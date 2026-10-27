package pricing

import "testing"

func TestIncludedTax(t *testing.T) {
	cases := []struct {
		amount, rate, want int64
	}{
		{1_180_00, 1800, 180_00},   // ₹1,180 incl. 18% → ₹180
		{1_299_900, 1800, 198_290}, // ₹12,999 → ₹1,982.90 (1,982.898 rounded half-up)
		{0, 1800, 0},
		{100, 0, 0},
	}
	for _, c := range cases {
		if got := IncludedTax(c.amount, c.rate); got != c.want {
			t.Errorf("IncludedTax(%d, %d) = %d, want %d", c.amount, c.rate, got, c.want)
		}
	}
}

func TestCompute(t *testing.T) {
	got := Compute(Input{
		Lines: []Line{
			{UnitPaise: 1_299_900, Quantity: 1, GSTRateBps: 1800},
			{UnitPaise: 1_849_900, Quantity: 1, GSTRateBps: 1800},
		},
		AddonPaise:      []int64{49_900},
		AddonGSTRateBps: 1800,
	})
	want := Totals{
		SubtotalPaise:    3_149_800,
		AddonsPaise:      49_900,
		TotalPaise:       3_199_700,
		TaxIncludedPaise: 198_290 + 282_188 + 7_612,
	}
	if got != want {
		t.Fatalf("Compute = %+v, want %+v", got, want)
	}
}

func TestComputeDiscountCappedAtSubtotal(t *testing.T) {
	got := Compute(Input{Lines: []Line{{UnitPaise: 1000, Quantity: 1}}, DiscountPaise: 5000})
	if got.DiscountPaise != 1000 || got.TotalPaise != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestRemaining(t *testing.T) {
	if Remaining(1_000_000, 1_500_000) != 500_000 || Remaining(2_000_000, 1_500_000) != 0 {
		t.Fatal("unexpected remaining")
	}
}
