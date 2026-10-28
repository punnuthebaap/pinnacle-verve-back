package app

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"testing"
	"time"
)

const orderTestSlug = "zenith-minimalist-round"

// placeOrder checks out one unit as a new shopper and optionally pays for it through the mock gateway.
func placeOrder(t *testing.T, s *stack, key string, pay bool) (ref, providerOrderID string, shopper *client) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: s.c.base, origin: "http://localhost:3000", http: &http.Client{Jar: jar}}
	var product struct {
		DefaultVariantID string `json:"defaultVariantId"`
	}
	c.do("GET", "/products/"+orderTestSlug, "", nil, 200, &product)
	c.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 1}, 200, nil)
	var created struct {
		Order   struct{ Ref string }
		Payment struct {
			ProviderOrderID string `json:"providerOrderId"`
		}
	}
	c.do("POST", "/orders", key, checkoutBody, 201, &created)
	if pay {
		c.do("POST", "/payments/mock/"+created.Payment.ProviderOrderID+"/complete", "", map[string]any{"outcome": "success"}, 204, nil)
	}
	return created.Order.Ref, created.Payment.ProviderOrderID, c
}

type adminOrder struct {
	Order struct {
		Ref, Status, PaymentStatus, FulfilmentStatus string
		Tracking                                     *struct{ Carrier, Number string }
	}
	Attention string
	Payment   struct{ Status, ProviderPaymentId string }
	Events    []struct {
		Type      string
		AdminName string
		Data      map[string]any
	}
}

func (o adminOrder) eventTypes() []string {
	var out []string
	for _, e := range o.Events {
		out = append(out, e.Type)
	}
	return out
}

// Order operations end to end: role checks, queues and search, forward-only fulfilment with tracking,
// cancellation rules and stock release, refund records, late-capture reinstatement, notes and audit.
func TestAdminOrders(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	for _, q := range []string{resetAdmins, "TRUNCATE orders CASCADE"} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stock := func() (n int) {
		if err := s.pool.QueryRow(ctx, `SELECT v.stock FROM product_variants v JOIN products p ON p.id = v.product_id
			WHERE p.slug = $1 ORDER BY v.price_paise LIMIT 1`, orderTestSlug).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	cat := adminClient(t, s, "catalog@example.com", "catalog")
	ops := adminClient(t, s, "orders@example.com", "orders")
	cat.do("GET", "/admin/orders", "", nil, 403, nil)

	paid, _, shopper := placeOrder(t, s, "a3-paid-000000001", true)
	unpaid, unpaidPOID, _ := placeOrder(t, s, "a3-unpaid-0000001", false)
	late, latePOID, _ := placeOrder(t, s, "a3-late-000000001", false)
	if _, err := s.pool.Exec(ctx, "UPDATE orders SET created_at = now() - interval '31 minutes' WHERE ref = $1", late); err != nil {
		t.Fatal(err)
	}
	if _, err := s.app.Orders.ExpirePending(ctx, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	s.c.do("POST", "/payments/mock/"+latePOID+"/complete", "", map[string]any{"outcome": "success"}, 204, nil)

	// Queues, counts and search.
	type page struct {
		Items  []struct{ Ref, Attention string }
		Total  int
		Counts struct{ ToFulfil, Attention int }
	}
	var p page
	ops.do("GET", "/admin/orders", "", nil, 200, &p)
	if p.Total != 3 || p.Counts.ToFulfil != 1 || p.Counts.Attention != 1 {
		t.Fatalf("list = %+v", p)
	}
	ops.do("GET", "/admin/orders?queue=to_fulfil", "", nil, 200, &p)
	if len(p.Items) != 1 || p.Items[0].Ref != paid {
		t.Fatalf("to_fulfil = %+v", p.Items)
	}
	ops.do("GET", "/admin/orders?queue=attention", "", nil, 200, &p)
	if len(p.Items) != 1 || p.Items[0].Ref != late || p.Items[0].Attention != "late_capture" {
		t.Fatalf("attention = %+v", p.Items)
	}
	ops.do("GET", "/admin/orders?q="+unpaid[3:], "", nil, 200, &p)
	if len(p.Items) != 1 || p.Items[0].Ref != unpaid {
		t.Fatalf("search by ref = %+v", p.Items)
	}
	ops.do("GET", "/admin/orders?q=98450&status=placed", "", nil, 200, &p)
	if len(p.Items) != 1 || p.Items[0].Ref != paid {
		t.Fatalf("search by phone + status = %+v", p.Items)
	}
	ops.do("GET", "/admin/orders/PV-NOTREAL1", "", nil, 404, nil)

	// Fulfilment: paid orders only, forward only, tracking once dispatched.
	// o is reset before each decode: omitted fields (attention, adminName) must not keep earlier values.
	var o adminOrder
	ops.do("POST", "/admin/orders/"+unpaid+"/fulfilment", "", map[string]any{"status": "preparing"}, 422, nil)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "preparing"}, 200, &o)
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "preparing", "carrier": "Delhivery", "trackingNumber": "DL123"}, 422, nil)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "in_transit", "carrier": "Delhivery", "trackingNumber": "DL123"}, 200, &o)
	if o.Order.FulfilmentStatus != "in_transit" || o.Order.Tracking == nil || o.Order.Tracking.Number != "DL123" {
		t.Fatalf("after dispatch = %+v", o.Order)
	}
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "in_transit", "carrier": "Delhivery", "trackingNumber": "DL124"}, 200, &o)
	if o.Order.Tracking.Number != "DL124" || o.Events[len(o.Events)-1].Type != "tracking_updated" {
		t.Fatalf("tracking correction = %+v", o)
	}
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "preparing"}, 422, nil)
	ops.do("POST", "/admin/orders/"+paid+"/cancel", "", map[string]any{"reason": "Customer changed mind"}, 422, nil)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+paid+"/fulfilment", "", map[string]any{"status": "delivered", "note": "Signed by concierge"}, 200, &o)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+paid+"/notes", "", map[string]any{"text": "Customer asked for an invoice copy."}, 200, &o)
	if last := o.Events[len(o.Events)-1]; last.Type != "note" || last.AdminName != "Test orders" {
		t.Fatalf("note event = %+v", last)
	}
	if o.Events[0].Type != "order_created" || o.Events[0].AdminName != "" {
		t.Fatalf("system events must have no admin: %+v", o.Events[0])
	}
	// The shopper sees the status and tracking on their order page.
	var mine struct {
		FulfilmentStatus string
		Tracking         *struct{ Carrier, Number string }
	}
	shopper.do("GET", "/orders/"+paid, "", nil, 200, &mine)
	if mine.FulfilmentStatus != "delivered" || mine.Tracking == nil || mine.Tracking.Carrier != "Delhivery" {
		t.Fatalf("shopper view = %+v", mine)
	}

	// Cancelling an unpaid order releases stock and closes the gateway order; a payment that still
	// arrives becomes a late capture, which can be reinstated while stock allows.
	before := stock()
	ops.do("POST", "/admin/orders/"+unpaid+"/cancel", "", map[string]any{"reason": "x"}, 400, nil) // reason too short (schema)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+unpaid+"/cancel", "", map[string]any{"reason": "Duplicate order"}, 200, &o)
	if o.Order.Status != "cancelled" || o.Order.PaymentStatus != "expired" || o.Payment.Status != "expired" || stock() != before+1 {
		t.Fatalf("cancel unpaid = %+v, stock %d (was %d)", o, stock(), before)
	}
	ops.do("POST", "/admin/orders/"+unpaid+"/cancel", "", map[string]any{"reason": "Duplicate order"}, 422, nil)
	s.c.do("POST", "/payments/mock/"+unpaidPOID+"/complete", "", map[string]any{"outcome": "success"}, 204, nil)
	o = adminOrder{}
	ops.do("GET", "/admin/orders/"+unpaid, "", nil, 200, &o)
	if o.Attention != "late_capture" {
		t.Fatalf("late payment after cancel not flagged: %+v", o)
	}
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+unpaid+"/reinstate", "", nil, 200, &o)
	if o.Order.Status != "placed" || o.Order.PaymentStatus != "paid" || o.Payment.Status != "captured" || o.Attention != "" || stock() != before {
		t.Fatalf("reinstate = %+v, stock %d (want %d)", o, stock(), before)
	}
	ops.do("POST", "/admin/orders/"+unpaid+"/reinstate", "", nil, 422, nil)

	// Late capture without stock: reinstating is refused, a refund is recorded instead.
	if _, err := s.pool.Exec(ctx, `UPDATE product_variants SET stock = 0 WHERE product_id = (SELECT id FROM products WHERE slug = $1)`, orderTestSlug); err != nil {
		t.Fatal(err)
	}
	ops.do("POST", "/admin/orders/"+late+"/reinstate", "", nil, 422, nil)
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+late+"/refund", "", map[string]any{"reference": "rfnd_late_001"}, 200, &o)
	if o.Order.PaymentStatus != "refunded" || o.Attention != "" {
		t.Fatalf("refund late capture = %+v", o)
	}
	ops.do("POST", "/admin/orders/"+late+"/refund", "", map[string]any{"reference": "rfnd_late_002"}, 422, nil)

	// A paid order cancelled before dispatch is "refund due" until the refund is recorded.
	ops.do("POST", "/admin/orders/"+unpaid+"/refund", "", map[string]any{"reference": "rfnd_early"}, 422, nil) // nothing owed yet
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+unpaid+"/cancel", "", map[string]any{"reason": "Out of delivery area"}, 200, &o)
	if o.Order.PaymentStatus != "paid" || o.Attention != "refund_due" {
		t.Fatalf("cancel paid = %+v", o)
	}
	ops.do("GET", "/admin/orders", "", nil, 200, &p)
	if p.Counts.Attention != 1 || p.Counts.ToFulfil != 0 {
		t.Fatalf("counts after cancel = %+v", p.Counts)
	}
	o = adminOrder{}
	ops.do("POST", "/admin/orders/"+unpaid+"/refund", "", map[string]any{"reference": "rfnd_paid_001", "note": "Full refund"}, 200, &o)
	if o.Order.PaymentStatus != "refunded" || o.Attention != "" {
		t.Fatalf("refund = %+v", o)
	}
	want := []string{"order_created", "order_cancelled", "payment_captured_after_expiry", "order_reinstated", "order_cancelled", "refund_recorded"}
	if !slices.Equal(o.eventTypes(), want) {
		t.Fatalf("timeline = %v, want %v", o.eventTypes(), want)
	}

	var audited int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit WHERE action LIKE 'order.%'").Scan(&audited); err != nil || audited != 10 {
		t.Fatalf("audit entries = %d (err %v), want 10", audited, err)
	}
}
