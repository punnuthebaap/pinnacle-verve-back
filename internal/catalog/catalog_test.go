package catalog

import "testing"

func TestVariantLabel(t *testing.T) {
	opts := []byte(`[
		{"code":"size","name":"Dimension","kind":"chip","values":[{"code":"l","label":"Large","detail":"36\" × 24\""}]},
		{"code":"finish","name":"Finish","kind":"swatch","values":[{"code":"brass","label":"Brushed Champagne Brass"}]}
	]`)
	got := VariantLabel(opts, []byte(`{"finish":"brass","size":"l"}`))
	if want := `Large 36" × 24" • Brushed Champagne Brass`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := VariantLabel([]byte(`[]`), []byte(`{}`)); got != "" {
		t.Fatalf("expected empty label, got %q", got)
	}
}
