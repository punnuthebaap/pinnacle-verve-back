package app

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"testing"
)

type storeSettings struct {
	WhiteGloveThresholdPaise, ShippingPaise int64
	AddonGstRateBps, PendingOrderTtlMinutes int
	PaymentMethods, SupportedPaymentMethods []string
	UpdatedBy                               string
}

// Owners change commercial values and add-ons; carts, checkout options and new orders follow at once.
func TestAdminSettings(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "TRUNCATE admin_users CASCADE"); err != nil {
		t.Fatal(err)
	}
	owner := adminClient(t, s, "owner@example.com", "owner")
	ops := adminClient(t, s, "orders@example.com", "orders")
	ops.do("GET", "/admin/settings", "", nil, 403, nil)
	ops.do("PUT", "/admin/addons/care-kit", "", map[string]any{"name": "Care kit", "description": "", "pricePaise": 1, "isActive": true, "position": 1}, 403, nil)

	// Defaults come from the environment configuration.
	var st storeSettings
	owner.do("GET", "/admin/settings", "", nil, 200, &st)
	if st.WhiteGloveThresholdPaise != 1_500_000 || st.ShippingPaise != 0 || st.AddonGstRateBps != 1800 || st.PendingOrderTtlMinutes != 30 ||
		len(st.PaymentMethods) != 4 || slices.Contains(st.SupportedPaymentMethods, "cod") || st.UpdatedBy != "" {
		t.Fatalf("defaults = %+v", st)
	}

	// A shopper with a ₹11,499 item and the wall mount in the bag.
	jar, _ := cookiejar.New(nil)
	shopper := &client{t: t, base: s.c.base, origin: "http://localhost:3000", http: &http.Client{Jar: jar}}
	var product struct {
		DefaultVariantID string `json:"defaultVariantId"`
	}
	shopper.do("GET", "/products/"+orderTestSlug, "", nil, 200, &product)
	shopper.do("POST", "/cart/items", "", map[string]any{"variantId": product.DefaultVariantID, "quantity": 1}, 200, nil)
	type cart struct {
		Totals struct {
			SubtotalPaise, ShippingPaise, AddonsPaise, TotalPaise int64
		}
		WhiteGlove struct{ ThresholdPaise, RemainingPaise int64 }
	}
	var before cart
	shopper.do("PUT", "/cart/addons", "", map[string]any{"codes": []string{"white-glove-mount"}}, 200, &before)

	// Schema-level rejections.
	valid := map[string]any{"whiteGloveThresholdPaise": 2_000_000, "shippingPaise": 49_900, "addonGstRateBps": 1800, "paymentMethods": []string{"upi", "card", "upi"}, "pendingOrderTtlMinutes": 45}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range valid {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	owner.do("PUT", "/admin/settings", "", with("paymentMethods", []string{}), 400, nil)
	owner.do("PUT", "/admin/settings", "", with("paymentMethods", []string{"cod"}), 400, nil)
	owner.do("PUT", "/admin/settings", "", with("pendingOrderTtlMinutes", 5), 400, nil)
	owner.do("PUT", "/admin/settings", "", with("shippingPaise", -1), 400, nil)

	owner.do("PUT", "/admin/settings", "", valid, 200, &st)
	if st.WhiteGloveThresholdPaise != 2_000_000 || st.ShippingPaise != 49_900 || !slices.Equal(st.PaymentMethods, []string{"upi", "card"}) ||
		st.PendingOrderTtlMinutes != 45 || st.UpdatedBy != "Test owner" {
		t.Fatalf("after update = %+v", st)
	}

	var opts struct {
		PaymentMethods           []string
		WhiteGloveThresholdPaise int64
	}
	s.c.do("GET", "/checkout-options", "", nil, 200, &opts)
	if !slices.Equal(opts.PaymentMethods, []string{"upi", "card"}) || opts.WhiteGloveThresholdPaise != 2_000_000 {
		t.Fatalf("checkout options = %+v", opts)
	}

	// Add-on price change and the new shipping charge apply to the open cart.
	var addons struct {
		Items []struct {
			Code       string
			PricePaise int64
			IsActive   bool
		}
	}
	owner.do("GET", "/admin/addons", "", nil, 200, &addons)
	if len(addons.Items) != 2 {
		t.Fatalf("addons = %+v", addons)
	}
	owner.do("PUT", "/admin/addons/white-glove-mount", "", map[string]any{"name": "White-Glove Wall Mount", "description": "Laser-leveled hanging.", "pricePaise": 99_900, "isActive": true, "position": 1}, 200, nil)
	owner.do("PUT", "/admin/addons/brass-polish", "", map[string]any{"name": "Brass polish", "description": "", "pricePaise": 29_900, "isActive": false, "position": 3}, 200, &addons)
	owner.do("PUT", "/admin/addons/Bad_Code", "", map[string]any{"name": "Nope", "description": "", "pricePaise": 1, "isActive": true, "position": 1}, 400, nil)
	if len(addons.Items) != 3 || addons.Items[2].Code != "brass-polish" || addons.Items[2].IsActive {
		t.Fatalf("addons after save = %+v", addons.Items)
	}
	var public struct{ Items []struct{ Code string } }
	s.c.do("GET", "/addons", "", nil, 200, &public)
	if len(public.Items) != 2 {
		t.Fatalf("switched-off add-on is offered: %+v", public.Items)
	}

	var after cart
	shopper.do("GET", "/cart", "", nil, 200, &after)
	if after.Totals.ShippingPaise != 49_900 || after.Totals.AddonsPaise != 99_900 ||
		after.Totals.TotalPaise != after.Totals.SubtotalPaise+49_900+99_900 || after.WhiteGlove.ThresholdPaise != 2_000_000 ||
		after.WhiteGlove.RemainingPaise != before.WhiteGlove.RemainingPaise+500_000 {
		t.Fatalf("cart before %+v, after %+v", before, after)
	}

	// Checkout enforces the enabled methods and charges what the cart showed.
	body := map[string]any{}
	for k, v := range checkoutBody {
		body[k] = v
	}
	body["paymentMethod"] = "netbanking"
	shopper.do("POST", "/orders", "a4-settings-key-0001", body, 422, nil)
	body["paymentMethod"] = "card"
	var created struct {
		Order struct{ Totals struct{ TotalPaise int64 } }
	}
	shopper.do("POST", "/orders", "a4-settings-key-0002", body, 201, &created)
	if created.Order.Totals.TotalPaise != after.Totals.TotalPaise {
		t.Fatalf("order total %d, cart showed %d", created.Order.Totals.TotalPaise, after.Totals.TotalPaise)
	}

	var audited int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit WHERE action IN ('settings.update', 'addon.update', 'addon.create')").Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("audit entries = %d (err %v), want 3", audited, err)
	}
}
