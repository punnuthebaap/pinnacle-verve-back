package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 197, G: 168, B: 128, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload posts raw bytes (the endpoint takes the file as the request body).
func upload(t *testing.T, c *client, productID, alt string, body []byte) (int, adminProductImages) {
	t.Helper()
	req, _ := http.NewRequest("POST", c.base+"/admin/products/"+productID+"/images?alt="+alt, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", c.origin)
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out adminProductImages
	if res.StatusCode != 201 && res.StatusCode != 422 && res.StatusCode != 413 {
		b, _ := io.ReadAll(res.Body)
		t.Logf("upload %d: %s", res.StatusCode, b)
	}
	if res.StatusCode == 201 {
		decode(t, res.Body, &out)
	}
	return res.StatusCode, out
}

type adminProductImages struct {
	Images []struct{ ID, URL, Alt string } `json:"images"`
}

func TestAdminImages(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, resetAdmins); err != nil {
		t.Fatal(err)
	}
	cat := adminClient(t, s, "catalog@example.com", "catalog")
	var list struct{ Items []struct{ ID string } }
	cat.do("GET", "/admin/products?q=ZEN-RND-40", "", nil, 200, &list)
	id := list.Items[0].ID
	before := s.revalidated.Load()

	if code, _ := upload(t, cat, id, "Plain%20text", []byte("not an image at all, just text")); code != 422 {
		t.Fatalf("text upload = %d, want 422", code)
	}
	if code, _ := upload(t, cat, id, "Huge", bytes.Repeat([]byte{0}, 10<<20+2048)); code != 413 {
		t.Fatalf("oversized upload = %d, want 413", code)
	}
	code, p := upload(t, cat, id, "Zenith%20detail", pngBytes(t))
	if code != 201 || len(p.Images) != 2 {
		t.Fatalf("upload = %d, images %+v", code, p.Images)
	}
	uploaded := p.Images[1]
	if !strings.HasPrefix(uploaded.URL, "/media/products/"+id+"/") || !strings.HasSuffix(uploaded.URL, ".png") {
		t.Fatalf("uploaded url = %q", uploaded.URL)
	}

	// Served from /media with long-lived caching.
	res, err := http.Get(strings.TrimSuffix(s.c.base, "/api/v1") + uploaded.URL)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("GET media: %v %v", res, err)
	}
	res.Body.Close()

	// Reorder (uploaded image first) and edit alt text; incomplete lists are refused.
	cat.do("PUT", "/admin/products/"+id+"/images", "", map[string]any{"images": []map[string]any{{"id": uploaded.ID, "alt": "Zenith close-up"}}}, 422, nil)
	cat.do("PUT", "/admin/products/"+id+"/images", "", map[string]any{"images": []map[string]any{
		{"id": uploaded.ID, "alt": "Zenith close-up"}, {"id": p.Images[0].ID, "alt": p.Images[0].Alt},
	}}, 200, nil)
	var live struct {
		Image  struct{ URL string } `json:"image"`
		Images []struct{ Alt string }
	}
	s.c.do("GET", "/products/zenith-minimalist-round", "", nil, 200, &live)
	if live.Image.URL != uploaded.URL || live.Images[0].Alt != "Zenith close-up" {
		t.Fatalf("storefront images = %+v", live)
	}

	// Deleting removes the row and the stored file.
	var afterDelete adminProductImages
	cat.do("DELETE", "/admin/products/"+id+"/images/"+uploaded.ID, "", nil, 200, &afterDelete)
	if len(afterDelete.Images) != 1 {
		t.Fatalf("after delete: %+v", afterDelete.Images)
	}
	stored := filepath.Join(s.mediaDir, filepath.FromSlash(strings.TrimPrefix(uploaded.URL, "/media/")))
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("file still on disk: %s (%v)", stored, err)
	}
	cat.do("DELETE", "/admin/products/"+id+"/images/"+uploaded.ID, "", nil, 404, nil)

	// Upload + reorder + delete = 3 successful writes → 3 revalidations; rejected ones none.
	waitFor(t, func() bool { return s.revalidated.Load()-before == 3 })
}

func decode(t *testing.T, r io.Reader, out any) {
	t.Helper()
	b, _ := io.ReadAll(r)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("decode: %v: %s", err, b)
	}
}

// waitFor polls cond for up to 3s (revalidation calls are asynchronous).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met within 3s")
}
