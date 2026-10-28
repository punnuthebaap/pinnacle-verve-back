package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/cart"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/payment"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/session"
	"github.com/pinnacleverve/api/internal/settings"
	"github.com/pinnacleverve/api/internal/store"
)

// Sign-in is throttled per client IP on top of the per-account lockout.
func TestRateLimits(t *testing.T) {
	s := newStack(t)
	for i := 1; i <= 20; i++ {
		// A different address each time, so the per-account lockout never kicks in.
		s.c.do("POST", "/admin/session", "", map[string]any{"email": fmt.Sprintf("nobody-%d@example.com", i), "password": "wrong-password-123"}, 401, nil)
	}
	req, _ := http.NewRequest("POST", s.c.base+"/admin/session", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := s.c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("21st sign-in = %d (Retry-After %q), want 429", res.StatusCode, res.Header.Get("Retry-After"))
	}
	// Other endpoints keep working for the same client.
	s.c.do("GET", "/products/"+orderTestSlug, "", nil, 200, nil)
}

type downGateway struct{ payment.Mock }

func (downGateway) CreateOrder(context.Context, string, int64) (payment.Session, error) {
	return payment.Session{}, errors.New("connection refused")
}

// The gateway order is created after the order transaction commits. If it fails, the order is removed
// and its stock released, and the same checkout can simply be retried.
func TestGatewayFailureAtCheckout(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg, _ := config.Load()
	q := store.New(s.pool)
	rules := settings.NewService(s.pool, q, admin.NewService(q, log), nil, settings.Defaults(cfg))

	jar, _ := cookiejar.New(nil)
	shopper := &client{t: t, base: s.c.base, origin: "http://localhost:3000", http: &http.Client{Jar: jar}}
	var product struct {
		DefaultVariantID string `json:"defaultVariantId"`
	}
	shopper.do("GET", "/products/"+orderTestSlug, "", nil, 200, &product)
	shopper.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 1}, 200, nil)
	stock := func() (n int) {
		if err := s.pool.QueryRow(ctx, "SELECT stock FROM product_variants WHERE id = $1", product.DefaultVariantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := stock()

	// Run checkout through a service whose gateway is down, as the shopper (their session cookie).
	down := order.NewService(s.pool, q, rules, downGateway{}, log)
	var createErr error
	h := session.Middleware(false, cart.Persister(q))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, createErr = down.Create(r.Context(), "gateway-down-key-01", order.CreateInput{
			Email: "buyer@example.com", PaymentMethod: "upi",
			Address: order.Address{FullName: "Test Buyer", Phone: "9845012890", Line1: "1 MG Road", City: "Bengaluru", State: "Karnataka", Pincode: "560001"},
		})
	}))
	u, _ := url.Parse(s.c.base)
	req := httptest.NewRequest("POST", "/api/v1/orders", nil)
	for _, c := range jar.Cookies(u) {
		req.AddCookie(c)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !errors.Is(createErr, order.ErrGatewayUnavailable) {
		t.Fatalf("create err = %v, want ErrGatewayUnavailable", createErr)
	}
	var orders int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM orders WHERE idempotency_key = 'gateway-down-key-01'").Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("failed order kept: %d (err %v)", orders, err)
	}
	if stock() != before {
		t.Fatalf("stock %d after failed checkout, want %d", stock(), before)
	}

	// Same key, gateway back: the order is created normally and the bag was kept.
	var created struct {
		Order   struct{ Ref string }
		Payment struct {
			ProviderOrderID string `json:"providerOrderId"`
		}
	}
	shopper.do("POST", "/orders", "gateway-down-key-01", checkoutBody, 201, &created)
	if created.Payment.ProviderOrderID == "" || stock() != before-1 {
		t.Fatalf("retry = %+v, stock %d", created, stock())
	}
}
