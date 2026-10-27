// Package pricing computes cart and order totals. All amounts are integer paise and GST-inclusive.
// It is pure (no I/O) so the same function prices the cart, the checkout and the persisted order.
package pricing

type Line struct {
	UnitPaise  int64
	Quantity   int64
	GSTRateBps int64 // 1800 = 18%
}

type Input struct {
	Lines           []Line
	AddonPaise      []int64
	AddonGSTRateBps int64
	ShippingPaise   int64
	DiscountPaise   int64
}

type Totals struct {
	SubtotalPaise    int64
	DiscountPaise    int64
	ShippingPaise    int64
	AddonsPaise      int64
	TaxIncludedPaise int64
	TotalPaise       int64
}

func LineTotal(l Line) int64 { return l.UnitPaise * l.Quantity }

// IncludedTax returns the GST portion of a tax-inclusive amount, rounded half-up to the paisa.
func IncludedTax(amount, rateBps int64) int64 {
	if amount <= 0 || rateBps <= 0 {
		return 0
	}
	den := 10_000 + rateBps
	return (amount*rateBps + den/2) / den
}

func Compute(in Input) Totals {
	var t Totals
	for _, l := range in.Lines {
		lt := LineTotal(l)
		t.SubtotalPaise += lt
		t.TaxIncludedPaise += IncludedTax(lt, l.GSTRateBps)
	}
	for _, a := range in.AddonPaise {
		t.AddonsPaise += a
		t.TaxIncludedPaise += IncludedTax(a, in.AddonGSTRateBps)
	}
	t.ShippingPaise = in.ShippingPaise
	// Discounts (coupons) arrive in M4; the tax effect of a discount is applied with them.
	t.DiscountPaise = min(in.DiscountPaise, t.SubtotalPaise)
	t.TotalPaise = t.SubtotalPaise + t.AddonsPaise + t.ShippingPaise - t.DiscountPaise
	return t
}

// Remaining returns how much more subtotal is needed to reach threshold (0 when reached).
func Remaining(subtotal, threshold int64) int64 {
	return max(0, threshold-subtotal)
}
