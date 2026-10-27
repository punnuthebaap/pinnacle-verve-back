package app

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/store"
)

// adminClient creates an admin with the given role and returns a signed-in client.
func adminClient(t *testing.T, s *stack, email, role string) *client {
	t.Helper()
	hash, _ := admin.HashPassword("test-password-123")
	if _, err := store.New(s.pool).InsertAdmin(context.Background(), store.InsertAdminParams{Email: email, Name: "Test " + role, PasswordHash: hash, Role: role}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: s.c.base, origin: "http://localhost:3000", http: &http.Client{Jar: jar}}
	c.do("POST", "/admin/session", "", map[string]any{"email": email, "password": "test-password-123"}, 200, nil)
	return c
}

type adminProduct struct {
	ID       string `json:"id"`
	IsActive bool   `json:"isActive"`
	Variants []struct {
		ID       string `json:"id"`
		Sku      string `json:"sku"`
		IsActive bool   `json:"isActive"`
	} `json:"variants"`
	Attributes []struct{ Key, Value string } `json:"attributes"`
	Related    []struct{ Slug string }       `json:"related"`
}

// Catalogue management end to end: roles, categories & filters, products, variants, filter values,
// pairings, validation, storefront visibility, audit and cache revalidation.
func TestAdminCatalog(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "TRUNCATE admin_users CASCADE"); err != nil {
		t.Fatal(err)
	}
	cat := adminClient(t, s, "catalog@example.com", "catalog")
	orders := adminClient(t, s, "orders@example.com", "orders")
	orders.do("GET", "/admin/products", "", nil, 403, nil)
	orders.do("GET", "/admin/categories", "", nil, 403, nil)

	var cats struct {
		Items []struct {
			ID, Slug string
			Facets   []struct{ Key string }
		}
	}
	cat.do("GET", "/admin/categories", "", nil, 200, &cats)
	if len(cats.Items) != 2 || len(cats.Items[0].Facets) != 3 {
		t.Fatalf("categories = %+v", cats)
	}

	// New category with one filter.
	newCat := map[string]any{"slug": "wall-sconces", "name": "Wall Sconces", "eyebrow": "", "description": "Brass wall lights.", "position": 3, "isActive": true}
	var sconces struct{ ID string }
	cat.do("POST", "/admin/categories", "", newCat, 201, &sconces)
	cat.do("POST", "/admin/categories", "", newCat, 409, nil) // slug taken
	cat.do("PUT", "/admin/categories/"+sconces.ID+"/facets", "", map[string]any{"facets": []map[string]any{
		{"key": "finish", "label": "Finish", "kind": "swatch"}, {"key": "finish", "label": "Again", "kind": "multi"},
	}}, 422, nil)
	cat.do("PUT", "/admin/categories/"+sconces.ID+"/facets", "", map[string]any{"facets": []map[string]any{
		{"key": "finish", "label": "Finish", "kind": "swatch"},
	}}, 200, nil)

	options := []map[string]any{{"code": "finish", "name": "Finish", "kind": "swatch", "values": []map[string]any{
		{"code": "brass", "label": "Brushed Brass", "swatchHex": "#C4A57B"}, {"code": "noir", "label": "Matte Noir", "swatchHex": "#1C1A17"},
	}}}
	product := map[string]any{
		"slug": "luna-stem-sconce", "categoryId": sconces.ID, "name": "Luna Stem Sconce", "subtitle": "Opal globe • G9 LED",
		"series": "", "materialLabel": "Solid Brass", "sizeLabel": "H 12\"", "description": "", "shippingNote": "",
		"badges": []map[string]any{{"label": "New", "tone": "dark"}}, "features": []any{}, "options": options,
		"specs": []any{}, "care": []any{}, "facts": []any{}, "gstRateBps": 1800, "rank": 1, "isActive": false,
	}
	var p adminProduct
	cat.do("POST", "/admin/products", "", product, 201, &p)
	cat.do("POST", "/admin/products", "", product, 409, nil)       // slug taken
	s.c.do("GET", "/products/luna-stem-sconce", "", nil, 404, nil) // inactive: hidden from the storefront

	variant := func(sku, finish string, price int) map[string]any {
		return map[string]any{"sku": sku, "options": map[string]string{"finish": finish}, "pricePaise": price, "mrpPaise": price + 100000, "stock": 5, "isActive": true}
	}
	put := func(vs ...map[string]any) map[string]any { return map[string]any{"variants": vs} }
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(variant("LUNA-BR", "gold", 899900)), 422, nil)                                        // unknown option value
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(variant("LUNA-BR", "brass", 899900), variant("LUNA-BR2", "brass", 899900)), 422, nil) // duplicate combination
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(variant("BIO-DUET-2", "brass", 899900)), 409, nil)                                    // SKU used by another product
	bad := variant("LUNA-BR", "brass", 899900)
	bad["mrpPaise"] = 100
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(bad), 422, nil) // MRP below price
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(variant("LUNA-BR", "brass", 899900), variant("LUNA-NR", "noir", 949900)), 200, &p)
	if len(p.Variants) != 2 {
		t.Fatalf("variants = %+v", p.Variants)
	}
	// Dropping a variant deactivates it instead of deleting it.
	keep := variant("LUNA-BR", "brass", 879900)
	keep["id"] = p.Variants[0].ID
	cat.do("PUT", "/admin/products/"+p.ID+"/variants", "", put(keep), 200, &p)
	if len(p.Variants) != 2 || !p.Variants[0].IsActive || p.Variants[1].IsActive {
		t.Fatalf("after dropping a variant: %+v", p.Variants)
	}

	attr := func(key string) map[string]any {
		return map[string]any{"attributes": []map[string]any{{"key": key, "value": "brushed-brass", "label": "Brushed Brass", "swatchHex": "#C4A57B"}}}
	}
	cat.do("PUT", "/admin/products/"+p.ID+"/attributes", "", attr("shape"), 422, nil) // not a filter of this category
	cat.do("PUT", "/admin/products/"+p.ID+"/attributes", "", attr("finish"), 200, &p)

	var aurelia struct{ Items []struct{ ID, Slug string } }
	cat.do("GET", "/admin/products?q=AUR-ARC", "", nil, 200, &aurelia) // search by SKU
	if len(aurelia.Items) != 1 || aurelia.Items[0].Slug != "aurelia-arc-desk-lamp" {
		t.Fatalf("SKU search = %+v", aurelia)
	}
	cat.do("PUT", "/admin/products/"+p.ID+"/related", "", map[string]any{"productIds": []string{p.ID}}, 422, nil) // self
	cat.do("PUT", "/admin/products/"+p.ID+"/related", "", map[string]any{"productIds": []string{aurelia.Items[0].ID}}, 200, &p)
	if len(p.Related) != 1 || len(p.Attributes) != 1 {
		t.Fatalf("product = %+v", p)
	}

	// Removing an option still used by an active variant is refused.
	noOpts := map[string]any{}
	for k, v := range product {
		noOpts[k] = v
	}
	noOpts["options"] = []any{}
	cat.do("PUT", "/admin/products/"+p.ID, "", noOpts, 422, nil)

	// Publish: the storefront now shows it with the live price.
	product["isActive"] = true
	cat.do("PUT", "/admin/products/"+p.ID, "", product, 200, &p)
	var live struct {
		PricePaise int64 `json:"pricePaise"`
		Variants   []struct{ Sku string }
	}
	s.c.do("GET", "/products/luna-stem-sconce", "", nil, 200, &live)
	if live.PricePaise != 879900 || len(live.Variants) != 1 {
		t.Fatalf("storefront product = %+v", live)
	}

	var audit struct{ Items []struct{ Action string } }
	owner := adminClient(t, s, "owner@example.com", "owner")
	owner.do("GET", "/admin/audit", "", nil, 200, &audit)
	actions := map[string]bool{}
	for _, e := range audit.Items {
		actions[e.Action] = true
	}
	for _, want := range []string{"category.create", "category.facets", "product.create", "product.variants", "product.attributes", "product.related", "product.update"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}

	// Each of the 8 successful writes revalidated the storefront (asynchronously); rejected writes did not.
	const writes = 8 // category create, facets, product create, 2× variants, attributes, related, publish
	deadline := time.Now().Add(3 * time.Second)
	for s.revalidated.Load() < writes && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let any unexpected extra calls land
	if n := s.revalidated.Load(); n != writes {
		t.Fatalf("storefront revalidated %d times, want %d", n, writes)
	}
}
