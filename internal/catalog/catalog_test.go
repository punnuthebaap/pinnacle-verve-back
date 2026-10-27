package catalog

import (
	"testing"

	"github.com/pinnacleverve/api/internal/store"
)

func TestSummarize(t *testing.T) {
	got := summarize([]store.ReviewDistributionRow{{Rating: 5, Reviews: 101}, {Rating: 4, Reviews: 9}, {Rating: 3, Reviews: 2}})
	if got.Count != 112 || got.Avg != 4.9 || got.Distribution != [5]int{101, 9, 2, 0, 0} {
		t.Fatalf("summarize = %+v", got)
	}
	if empty := summarize(nil); empty.Count != 0 || empty.Avg != 0 {
		t.Fatalf("empty summary = %+v", empty)
	}
}

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
