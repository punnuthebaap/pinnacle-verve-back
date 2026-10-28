package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Review moderation keeps product ratings in step; the newsletter list can be searched, exported
// (CSV-injection safe, audited) and pruned.
func TestAdminMarketing(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, resetAdmins); err != nil {
		t.Fatal(err)
	}
	cat := adminClient(t, s, "catalog@example.com", "catalog")
	mod := adminClient(t, s, "content@example.com", "content")
	cat.do("GET", "/admin/reviews", "", nil, 403, nil)
	cat.do("GET", "/admin/newsletter", "", nil, 403, nil)

	const slug = "contour-architectural-vanity-mirror"
	var reviewID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO product_reviews (product_id, rating, author_name, body, status)
		SELECT id, 1, 'Test Shopper', 'Arrived with a chipped corner.', 'pending' FROM products WHERE slug = $1 RETURNING id`, slug).Scan(&reviewID); err != nil {
		t.Fatal(err)
	}
	type product struct {
		RatingAvg   float64
		RatingCount int
	}
	var before product
	s.c.do("GET", "/products/"+slug, "", nil, 200, &before)

	type page struct {
		Items []struct {
			ID, Status, Body string
			Product          struct{ Slug string }
			ModeratedBy      string
		}
		Total, Pending int
	}
	var p page
	mod.do("GET", "/admin/reviews?status=pending", "", nil, 200, &p)
	if p.Total != 1 || p.Pending != 1 || p.Items[0].ID != reviewID || p.Items[0].Product.Slug != slug {
		t.Fatalf("pending = %+v", p)
	}
	mod.do("GET", "/admin/reviews?q=chipped", "", nil, 200, &p)
	if p.Total != 1 {
		t.Fatalf("search = %+v", p)
	}
	var all page
	mod.do("GET", "/admin/reviews?writtenOnly=false&page=1", "", nil, 200, &all)
	mod.do("GET", "/admin/reviews", "", nil, 200, &p)
	if all.Total <= p.Total {
		t.Fatalf("rating-only entries should be hidden by default: all %d, written %d", all.Total, p.Total)
	}

	// Publishing counts the rating and shows the review; rejecting takes both back out.
	revalidated := s.revalidated.Load()
	mod.do("PATCH", "/admin/reviews/"+reviewID, "", map[string]any{"status": "published"}, 204, nil)
	var after product
	s.c.do("GET", "/products/"+slug, "", nil, 200, &after)
	if after.RatingCount != before.RatingCount+1 || after.RatingAvg > before.RatingAvg { // avg is rounded to 0.1
		t.Fatalf("rating before %+v, after publish %+v", before, after)
	}
	var reviews struct{ Items []struct{ Body string } }
	s.c.do("GET", "/products/"+slug+"/reviews", "", nil, 200, &reviews)
	if len(reviews.Items) == 0 || reviews.Items[0].Body != "Arrived with a chipped corner." {
		t.Fatalf("published review not listed first: %+v", reviews.Items)
	}
	waitFor(t, func() bool { return s.revalidated.Load() > revalidated })
	mod.do("PATCH", "/admin/reviews/"+reviewID, "", map[string]any{"status": "rejected"}, 204, nil)
	s.c.do("GET", "/products/"+slug, "", nil, 200, &after)
	if after != before {
		t.Fatalf("rating after reject %+v, want %+v", after, before)
	}
	mod.do("GET", "/admin/reviews?status=rejected", "", nil, 200, &p)
	if len(p.Items) != 1 || p.Items[0].ModeratedBy != "Test content" || p.Pending != 0 {
		t.Fatalf("rejected = %+v", p)
	}
	mod.do("PATCH", "/admin/reviews/"+reviewID, "", map[string]any{"status": "hidden"}, 400, nil)
	mod.do("PATCH", "/admin/reviews/00000000-0000-0000-0000-000000000001", "", map[string]any{"status": "published"}, 404, nil)

	// Newsletter.
	s.c.do("POST", "/newsletter/subscriptions", "", map[string]any{"email": "Asha@Example.com"}, 202, nil)
	if _, err := s.pool.Exec(ctx, `INSERT INTO newsletter_subscribers (email) VALUES ('=hyperlink@example.com')`); err != nil {
		t.Fatal(err)
	}
	var subs struct {
		Items []struct{ Email string }
		Total int
	}
	mod.do("GET", "/admin/newsletter?q=asha", "", nil, 200, &subs)
	if subs.Total != 1 || subs.Items[0].Email != "asha@example.com" {
		t.Fatalf("subscribers = %+v", subs)
	}
	req, _ := http.NewRequest("GET", mod.base+"/admin/newsletter/export", nil)
	res, err := mod.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csv := string(body)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(csv, "email,subscribed_at\n") || !strings.Contains(csv, "asha@example.com,") || !strings.Contains(csv, "'=hyperlink@example.com,") {
		t.Fatalf("export %d %q:\n%s", res.StatusCode, res.Header.Get("Content-Type"), csv)
	}

	mod.do("DELETE", "/admin/newsletter/ASHA@example.com", "", nil, 204, nil)
	mod.do("DELETE", "/admin/newsletter/asha@example.com", "", nil, 404, nil)

	var actions []string
	rows, err := s.pool.Query(ctx, "SELECT action || ':' || entity_id FROM admin_audit WHERE action LIKE 'review.%' OR action LIKE 'newsletter.%' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	want := "review.status:" + reviewID + " review.status:" + reviewID + " newsletter.export: newsletter.remove:a***@example.com"
	if strings.Join(actions, " ") != want {
		t.Fatalf("audit = %v\nwant    %s", actions, want)
	}
}
