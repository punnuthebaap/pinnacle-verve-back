package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// A payment webhook and a staff cancellation racing on the same order must both finish (no deadlock,
// no 500) and leave a consistent state: whichever wins, the money is flagged for follow-up and the
// stock is released exactly once.
func TestWebhookAndAdminCancelRace(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, resetAdmins); err != nil {
		t.Fatal(err)
	}
	ops := adminClient(t, s, "orders@example.com", "orders")
	stock := func() (n int) {
		if err := s.pool.QueryRow(ctx, `SELECT v.stock FROM product_variants v JOIN products p ON p.id = v.product_id
			WHERE p.slug = $1 ORDER BY v.price_paise LIMIT 1`, orderTestSlug).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	start := stock()

	const rounds = 8
	for i := 0; i < rounds; i++ {
		ref, poid, _ := placeOrder(t, s, fmt.Sprintf("race-key-%010d", i), false)
		var wg sync.WaitGroup
		var payStatus, cancelStatus int
		wg.Add(2)
		go func() {
			defer wg.Done()
			payStatus = s.c.raw("POST", "/payments/mock/"+poid+"/complete", map[string]any{"outcome": "success"})
		}()
		go func() {
			defer wg.Done()
			cancelStatus = ops.raw("POST", "/admin/orders/"+ref+"/cancel", map[string]any{"reason": "Race test"})
		}()
		wg.Wait()
		if payStatus != 204 || cancelStatus != 200 {
			t.Fatalf("round %d: webhook %d, cancel %d", i, payStatus, cancelStatus)
		}
		var o adminOrder
		ops.do("GET", "/admin/orders/"+ref, "", nil, 200, &o)
		if o.Order.Status != "cancelled" || (o.Attention != "refund_due" && o.Attention != "late_capture") {
			t.Fatalf("round %d: inconsistent state %+v", i, o.Order)
		}
	}
	if got := stock(); got != start {
		t.Fatalf("stock %d after %d cancelled orders, want %d (released exactly once each)", got, rounds, start)
	}
}

// raw sends a JSON request and returns the status (safe from goroutines: it never calls t.Fatal).
func (c *client) raw(method, path string, body any) int {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	res, err := c.http.Do(req)
	if err != nil {
		return -1
	}
	res.Body.Close()
	return res.StatusCode
}
