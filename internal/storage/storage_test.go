package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalPutServeDelete(t *testing.T) {
	dir := t.TempDir()
	l := Local{Dir: dir, URLPrefix: "/media"}
	url, err := l.Put(context.Background(), "products/abc/1f2e.png", strings.NewReader("png-bytes"))
	if err != nil || url != "/media/products/abc/1f2e.png" {
		t.Fatalf("Put = %q, %v", url, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "products", "abc", "1f2e.png")); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(l.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + url)
	if err != nil || res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("serve: %v %v", res, err)
	}
	res.Body.Close()
	for _, bad := range []string{"/media/products/", "/media/../go.mod", "/media/products/abc/x.html"} {
		if res, _ := http.Get(srv.URL + bad); res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", bad, res.StatusCode)
		}
	}

	if err := l.Delete(context.Background(), "products/abc/1f2e.png"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(context.Background(), "products/abc/1f2e.png"); err != nil {
		t.Fatalf("deleting a missing file should be a no-op: %v", err)
	}
}

func TestKeysCannotEscapeRoot(t *testing.T) {
	l := Local{Dir: t.TempDir(), URLPrefix: "/media"}
	for _, key := range []string{"../x.png", "products/../../x.png", "/abs.png", "x.exe", "Products/X.PNG", "a//b.png"} {
		if _, err := l.Put(context.Background(), key, strings.NewReader("x")); err != ErrBadKey {
			t.Errorf("Put(%q) err = %v, want ErrBadKey", key, err)
		}
	}
}
