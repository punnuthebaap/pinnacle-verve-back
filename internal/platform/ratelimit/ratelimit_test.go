package ratelimit

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterWindows(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	l := New([]Rule{{Name: "signin", Limit: 2, Window: time.Minute, Match: Post("/login")}}, func(r *http.Request) string { return r.RemoteAddr }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	l.now = func() time.Time { return now }
	req := func(ip, method, path string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = ip
		return r
	}
	for i := 0; i < 2; i++ {
		if _, _, ok := l.Allow(req("a", "POST", "/login")); !ok {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if _, wait, ok := l.Allow(req("a", "POST", "/login")); ok || wait != time.Minute {
		t.Fatalf("third request: ok=%v wait=%v", ok, wait)
	}
	if _, _, ok := l.Allow(req("b", "POST", "/login")); !ok {
		t.Fatal("another client must not be affected")
	}
	if _, _, ok := l.Allow(req("a", "GET", "/login")); !ok {
		t.Fatal("unmatched requests are not limited")
	}
	now = now.Add(time.Minute)
	if _, _, ok := l.Allow(req("a", "POST", "/login")); !ok {
		t.Fatal("new window should allow again")
	}

	rec := httptest.NewRecorder()
	l.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req("a", "POST", "/login"))
	l.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req("a", "POST", "/login"))
	rec = httptest.NewRecorder()
	l.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req("a", "POST", "/login"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("middleware = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := ParseCIDRs([]string{"127.0.0.1", "::1", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	ip := ClientIP(trusted)
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},              // untrusted peer: header ignored
		{"127.0.0.1:5000", "198.51.100.7", "198.51.100.7"},          // via the storefront
		{"127.0.0.1:5000", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // spoofed left-most hop ignored
		{"[::1]:5000", "198.51.100.7, 10.1.2.3", "198.51.100.7"},    // internal hops skipped
		{"127.0.0.1:5000", "", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ip(r); got != c.want {
			t.Errorf("ClientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
