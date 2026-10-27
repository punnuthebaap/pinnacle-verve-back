package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/db"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
)

// stack is a freshly seeded API backed by a real Postgres (integration tests run serially).
type stack struct {
	app         *App
	pool        *pgxpool.Pool
	c           *client
	revalidated *atomic.Int32 // storefront revalidation calls received
}

// newStack uses TEST_DATABASE_URL (default: local dev cluster, database pinnacle_test, created if missing).
func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@localhost:5433/pinnacle_test?sslmode=disable"
		if err := ensureDatabase(ctx, "postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable", "pinnacle_test"); err != nil {
			t.Skipf("no test database available: %v", err)
		}
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Skipf("no test database available: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, db.DevSeed); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	cfg.DatabaseURL = url
	// Stand-in storefront: counts authenticated revalidation calls.
	var revalidated atomic.Int32
	storefront := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Revalidate-Secret") == cfg.RevalidateSecret {
			revalidated.Add(1)
		}
	}))
	t.Cleanup(storefront.Close)
	cfg.StorefrontRevalidateURL = storefront.URL
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), pool)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &stack{app: a, pool: pool, revalidated: &revalidated, c: &client{t: t, base: srv.URL + "/api/v1", origin: "http://localhost:3000", http: &http.Client{Jar: jar}}}
}

var checkoutBody = map[string]any{
	"email":         "buyer@example.com",
	"paymentMethod": "upi",
	"shippingAddress": map[string]any{
		"fullName": "Test Buyer", "phone": "9845012890", "line1": "Penthouse 402, Aura Manor",
		"city": "Bengaluru", "state": "Karnataka", "pincode": "560038",
	},
}

// Unpaid orders expire, release stock, and a capture arriving afterwards is recorded but not accepted.
func TestPendingOrderExpiry(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	var product struct {
		DefaultVariantID string `json:"defaultVariantId"`
	}
	s.c.do("GET", "/products/zenith-minimalist-round", "", nil, 200, &product)
	stock := func() (n int) {
		if err := s.pool.QueryRow(ctx, "SELECT stock FROM product_variants WHERE id = $1", product.DefaultVariantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := stock()

	var cart struct{ Notes string }
	s.c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 2}, 200, nil)
	s.c.do("PUT", "/cart/notes", "", map[string]any{"notes": "  Fluted wood paneling; use toggle anchors.  "}, 200, &cart)
	if cart.Notes != "Fluted wood paneling; use toggle anchors." {
		t.Fatalf("notes not trimmed/saved: %q", cart.Notes)
	}
	var created struct {
		Order struct {
			Ref   string `json:"ref"`
			Notes string `json:"notes"`
		} `json:"order"`
		Payment struct {
			ProviderOrderID string `json:"providerOrderId"`
		} `json:"payment"`
	}
	s.c.do("POST", "/orders", "expiry-test-key-0001", checkoutBody, 201, &created)
	if created.Order.Notes != cart.Notes {
		t.Fatalf("order did not inherit cart notes: %q", created.Order.Notes)
	}
	if got := stock(); got != before-2 {
		t.Fatalf("stock after reserve = %d, want %d", got, before-2)
	}

	// Not yet expired: nothing happens.
	if n, err := s.app.Orders.ExpirePending(ctx, 30*time.Minute); err != nil || n != 0 {
		t.Fatalf("premature expiry: n=%d err=%v", n, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE orders SET created_at = now() - interval '31 minutes' WHERE ref = $1", created.Order.Ref); err != nil {
		t.Fatal(err)
	}
	if n, err := s.app.Orders.ExpirePending(ctx, 30*time.Minute); err != nil || n != 1 {
		t.Fatalf("expire: n=%d err=%v", n, err)
	}
	if got := stock(); got != before {
		t.Fatalf("stock after expiry = %d, want %d", got, before)
	}
	var order struct{ Status, PaymentStatus string }
	s.c.do("GET", "/orders/"+created.Order.Ref, "", nil, 200, &order)
	if order.Status != "cancelled" || order.PaymentStatus != "expired" {
		t.Fatalf("order after expiry: %+v", order)
	}

	// Late capture: order stays cancelled; an event flags it for a manual refund.
	s.c.do("POST", "/payments/mock/"+created.Payment.ProviderOrderID+"/complete", "", map[string]any{"outcome": "success"}, 204, nil)
	s.c.do("GET", "/orders/"+created.Order.Ref, "", nil, 200, &order)
	if order.Status != "cancelled" || order.PaymentStatus != "expired" {
		t.Fatalf("late capture changed the order: %+v", order)
	}
	var flagged bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM order_events e JOIN orders o ON o.id = e.order_id
		WHERE o.ref = $1 AND e.type = 'payment_captured_after_expiry')`, created.Order.Ref).Scan(&flagged); err != nil || !flagged {
		t.Fatalf("late capture not flagged (err %v)", err)
	}
}

// Integration test: runs the full purchase journey against a real Postgres.
func TestPurchaseJourney(t *testing.T) {
	s := newStack(t)
	ctx, pool, c := context.Background(), s.pool, s.c
	c.origin = ""

	var health map[string]string
	c.do("GET", "/health", "", nil, 200, &health)

	var list struct {
		Items []struct{ Slug string } `json:"items"`
		Total int                     `json:"total"`
	}
	c.do("GET", "/products?category=mirrors&pageSize=48", "", nil, 200, &list)
	if list.Total != 8 {
		t.Fatalf("mirrors total = %d, want 8", list.Total)
	}
	c.do("GET", "/products?category=mirrors&filter=shape:organic-freeform&filter=material:hand-forged-brass", "", nil, 200, &list)
	if list.Total != 1 || list.Items[0].Slug != "contour-architectural-vanity-mirror" {
		t.Fatalf("facet filter returned %+v", list)
	}
	c.do("GET", "/products?sort=bogus", "", nil, 400, nil)

	var product struct {
		DefaultVariantID string `json:"defaultVariantId"`
		PricePaise       int64  `json:"pricePaise"`
		Variants         []struct{ ID string }
		Options          []struct{ Code string }
	}
	c.do("GET", "/products/contour-architectural-vanity-mirror", "", nil, 200, &product)
	if len(product.Variants) != 24 || len(product.Options) != 3 || product.PricePaise != 1_299_900 {
		t.Fatalf("unexpected product: %d variants, %d options, price %d", len(product.Variants), len(product.Options), product.PricePaise)
	}
	c.do("GET", "/products/does-not-exist", "", nil, 404, nil)

	var content struct {
		Care  []struct{ Title string }
		Facts []struct{ Label string }
		Specs []struct{ Bullets []string }
	}
	c.do("GET", "/products/contour-architectural-vanity-mirror", "", nil, 200, &content)
	if len(content.Care) != 6 || len(content.Facts) != 2 || len(content.Specs) != 1 || len(content.Specs[0].Bullets) != 3 {
		t.Fatalf("unexpected product content: %+v", content)
	}

	var reviews struct {
		Summary struct {
			RatingAvg    float64
			RatingCount  int
			Distribution []struct{ Stars, Count int }
		}
		Items []struct{ AuthorName string }
		Total int
	}
	c.do("GET", "/products/contour-architectural-vanity-mirror/reviews?pageSize=1", "", nil, 200, &reviews)
	if reviews.Summary.RatingCount != 112 || reviews.Summary.RatingAvg != 4.9 || len(reviews.Summary.Distribution) != 5 ||
		reviews.Summary.Distribution[0].Stars != 5 || reviews.Total != 2 || len(reviews.Items) != 1 || reviews.Items[0].AuthorName != "Suhasini Mehta" {
		t.Fatalf("unexpected reviews: %+v", reviews)
	}
	c.do("GET", "/products/does-not-exist/reviews", "", nil, 404, nil)
	c.do("GET", "/products/contour-architectural-vanity-mirror/reviews?pageSize=50", "", nil, 400, nil)

	var related struct{ Items []struct{ Slug string } }
	c.do("GET", "/products/contour-architectural-vanity-mirror/related", "", nil, 200, &related)
	if len(related.Items) != 3 || related.Items[0].Slug != "aurelia-arc-desk-lamp" {
		t.Fatalf("unexpected related: %+v", related)
	}

	// CSRF: foreign origin rejected; validation errors are problem+json.
	c.origin = "https://evil.example"
	c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 1}, 403, nil)
	c.origin = "http://localhost:3000"
	c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 0}, 400, nil)
	c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 6}, 409, nil) // stock is 5

	var cart struct {
		Items  []struct{ ID string } `json:"items"`
		Totals struct {
			AddonsPaise int64 `json:"addonsPaise"`
			TotalPaise  int64 `json:"totalPaise"`
		} `json:"totals"`
	}
	c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 2}, 200, &cart)
	if len(cart.Items) != 1 || cart.Totals.TotalPaise != 2_599_800 {
		t.Fatalf("cart after add: %+v", cart)
	}
	c.do("PATCH", "/cart/items/"+cart.Items[0].ID, "", map[string]any{"quantity": 1}, 200, &cart)
	c.do("PUT", "/cart/addons", "", map[string]any{"codes": []string{"white-glove-mount"}}, 200, &cart)
	if cart.Totals.AddonsPaise != 49_900 || cart.Totals.TotalPaise != 1_349_800 {
		t.Fatalf("cart after add-on: %+v", cart.Totals)
	}

	body := map[string]any{
		"email":         "buyer@example.com",
		"paymentMethod": "upi",
		"shippingAddress": map[string]any{
			"fullName": "Test Buyer", "phone": "9845012890", "line1": "Penthouse 402, Aura Manor",
			"city": "Bengaluru", "state": "Karnataka", "pincode": "560038",
		},
	}
	c.do("POST", "/orders", "", body, 400, nil) // missing Idempotency-Key
	var created struct {
		Order struct {
			Ref    string `json:"ref"`
			Status string `json:"status"`
		} `json:"order"`
		Payment struct {
			ProviderOrderID string `json:"providerOrderId"`
			AmountPaise     int64  `json:"amountPaise"`
		} `json:"payment"`
	}
	const key = "test-key-0123456789"
	c.do("POST", "/orders", key, body, 201, &created)
	if created.Order.Status != "pending_payment" || created.Payment.AmountPaise != 1_349_800 {
		t.Fatalf("created order: %+v", created)
	}
	firstRef := created.Order.Ref
	c.do("POST", "/orders", key, body, 201, &created) // idempotent replay
	if created.Order.Ref != firstRef {
		t.Fatalf("replay created a new order %s != %s", created.Order.Ref, firstRef)
	}

	// Forged webhook is rejected; mock completion goes through the signed webhook path.
	c.do("POST", "/payments/webhooks/mock", "", map[string]any{"id": "evt_x", "type": "payment.captured"}, 401, nil)
	c.do("POST", "/payments/mock/"+created.Payment.ProviderOrderID+"/complete", "", map[string]any{"outcome": "success"}, 204, nil)

	var order struct {
		Status, PaymentStatus string
		Items                 []struct{ VariantLabel string } `json:"items"`
	}
	c.do("GET", "/orders/"+firstRef, "", nil, 200, &order)
	if order.Status != "placed" || order.PaymentStatus != "paid" || !strings.Contains(order.Items[0].VariantLabel, "Large") {
		t.Fatalf("order after payment: %+v", order)
	}
	c.do("GET", "/cart", "", nil, 200, &cart)
	if len(cart.Items) != 0 {
		t.Fatal("cart not cleared after payment")
	}
	var stock int
	if err := pool.QueryRow(ctx, "SELECT stock FROM product_variants WHERE id = $1", product.DefaultVariantID).Scan(&stock); err != nil || stock != 4 {
		t.Fatalf("stock = %d (err %v), want 4", stock, err)
	}

	// Another session cannot read the order.
	other := &client{t: t, base: c.base, http: &http.Client{}}
	other.do("GET", "/orders/"+firstRef, "", nil, 404, nil)
}

type client struct {
	t      *testing.T
	base   string
	origin string
	http   *http.Client
}

func (c *client) do(method, path, idemKey string, body any, want int, out any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
}

func ensureDatabase(ctx context.Context, adminURL, name string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	}
	return err
}
