package app

import (
	"context"
	"testing"
)

func siteContent(navHref string) map[string]any {
	return map[string]any{
		"announcement":  []string{"Free white-glove delivery across India"},
		"primaryNav":    []map[string]any{{"label": "Home", "href": "/"}, {"label": "Collections", "href": navHref}},
		"footerColumns": []map[string]any{{"title": "Shop", "links": []map[string]any{{"label": "All Mirrors", "href": "/collections/mirrors"}}}},
		"legalLinks":    []map[string]any{{"label": "Privacy Policy", "href": "/privacy-policy"}},
		"copyright":     "© 2026 Pinnacle Verve Living Pvt. Ltd.",
		"paymentBadges": []string{"UPI", "Cards"},
	}
}

// Editors save site copy, homepage text and pages; links are restricted; drafts stay private.
func TestAdminContent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, resetAdmins); err != nil {
		t.Fatal(err)
	}
	cat := adminClient(t, s, "catalog@example.com", "catalog")
	ed := adminClient(t, s, "content@example.com", "content")
	cat.do("GET", "/admin/content/site", "", nil, 403, nil)
	cat.do("PUT", "/admin/content/pages/privacy-policy", "", map[string]any{"title": "Privacy", "body": "", "published": false}, 403, nil)

	// Nothing saved yet: the storefront uses its bundled copy.
	s.c.do("GET", "/content/site", "", nil, 404, nil)
	ed.do("GET", "/admin/content/site", "", nil, 404, nil)

	ed.do("PUT", "/admin/content/site", "", siteContent("javascript:alert(1)"), 422, nil)
	ed.do("PUT", "/admin/content/site", "", siteContent("//evil.example"), 422, nil)
	empty := siteContent("/collections")
	empty["primaryNav"] = []map[string]any{}
	ed.do("PUT", "/admin/content/site", "", empty, 400, nil)

	revalidated := s.revalidated.Load()
	var saved struct {
		Content struct {
			PrimaryNav []struct{ Label, Href string }
		}
		UpdatedBy string
	}
	ed.do("PUT", "/admin/content/site", "", siteContent("/collections"), 200, &saved)
	if saved.UpdatedBy != "Test content" || len(saved.Content.PrimaryNav) != 2 {
		t.Fatalf("saved = %+v", saved)
	}
	waitFor(t, func() bool { return s.revalidated.Load() > revalidated })
	var public struct{ Copyright string }
	s.c.do("GET", "/content/site", "", nil, 200, &public)
	if public.Copyright != "© 2026 Pinnacle Verve Living Pvt. Ltd." {
		t.Fatalf("public site = %+v", public)
	}

	section := map[string]any{"eyebrow": "Atmospheres", "title": "Curate Room by Room", "body": ""}
	home := map[string]any{
		"hero": map[string]any{"badge": "New", "titleBefore": "Reflections that", "titleEmphasis": "elevate", "titleAfter": "your space.", "body": "Mirrors and lamps.", "assurances": []string{"Free shipping"}},
		"sections": map[string]any{
			"collections": section, "bestSellers": section, "rooms": section, "pairings": section,
			"craftsmanship": section, "sizeGuide": section, "testimonials": section, "community": section,
		},
		"testimonials": []map[string]any{{"quote": "Beautiful.", "name": "A. K.", "meta": "Mumbai"}},
	}
	ed.do("PUT", "/admin/content/home", "", home, 200, nil)
	var h struct {
		Hero struct{ TitleEmphasis string }
	}
	s.c.do("GET", "/content/home", "", nil, 200, &h)
	if h.Hero.TitleEmphasis != "elevate" {
		t.Fatalf("home = %+v", h)
	}

	// Pages: drafts are private; publishing needs text.
	s.c.do("GET", "/content/pages/privacy-policy", "", nil, 404, nil)
	ed.do("PUT", "/admin/content/pages/privacy-policy", "", map[string]any{"title": "Privacy Policy", "body": "", "published": true}, 422, nil)
	ed.do("PUT", "/admin/content/pages/privacy-policy", "", map[string]any{"title": "Privacy Policy", "body": "## Data we collect\r\n\r\nYour email.", "published": false}, 200, nil)
	s.c.do("GET", "/content/pages/privacy-policy", "", nil, 404, nil)
	var page struct{ Title, Body string }
	ed.do("PUT", "/admin/content/pages/privacy-policy", "", map[string]any{"title": "Privacy Policy", "body": "## Data we collect\r\n\r\nYour email.", "published": true}, 200, nil)
	s.c.do("GET", "/content/pages/privacy-policy", "", nil, 200, &page)
	if page.Body != "## Data we collect\n\nYour email." {
		t.Fatalf("page body not normalised: %q", page.Body)
	}
	var list struct{ Items []struct{ Slug string } }
	ed.do("GET", "/admin/content/pages", "", nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].Slug != "privacy-policy" {
		t.Fatalf("pages = %+v", list)
	}
	ed.do("PUT", "/admin/content/pages/Bad_Slug", "", map[string]any{"title": "x", "body": "", "published": false}, 400, nil)

	var audited int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit WHERE action IN ('content.update', 'page.update')").Scan(&audited); err != nil || audited != 4 {
		t.Fatalf("audit entries = %d (err %v), want 4", audited, err)
	}
}
