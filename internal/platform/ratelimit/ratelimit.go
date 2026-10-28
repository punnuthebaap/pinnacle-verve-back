// Package ratelimit throttles abuse-prone endpoints per client IP (fixed windows, in memory).
//
// Limits are per API instance. With several instances behind a load balancer, the effective limit is
// multiplied by the instance count; move the counters to a shared store (Redis or Postgres) when hosting
// is chosen (ROADMAP B9).
package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pinnacleverve/api/internal/platform/httpx"
)

type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
	Match  func(*http.Request) bool
}

type window struct {
	start time.Time
	count int
}

type Limiter struct {
	rules    []Rule
	clientIP func(*http.Request) string
	log      *slog.Logger
	now      func() time.Time

	mu   sync.Mutex
	hits map[string]*window
}

// maxKeys bounds memory under a flood of distinct IPs; expired windows are swept when it's reached.
const maxKeys = 100_000

func New(rules []Rule, clientIP func(*http.Request) string, log *slog.Logger) *Limiter {
	return &Limiter{rules: rules, clientIP: clientIP, log: log, now: time.Now, hits: map[string]*window{}}
}

// Allow counts a request against the first matching rule. It returns the wait before the next allowed
// request when the limit is exceeded.
func (l *Limiter) Allow(r *http.Request) (rule string, retryAfter time.Duration, ok bool) {
	for _, rule := range l.rules {
		if !rule.Match(r) {
			continue
		}
		key := rule.Name + "|" + l.clientIP(r)
		now := l.now()
		l.mu.Lock()
		defer l.mu.Unlock()
		w := l.hits[key]
		if w == nil || now.Sub(w.start) >= rule.Window {
			if len(l.hits) >= maxKeys {
				l.sweep(now)
			}
			w = &window{start: now}
			l.hits[key] = w
		}
		w.count++
		if w.count > rule.Limit {
			return rule.Name, w.start.Add(rule.Window).Sub(now), false
		}
		return rule.Name, 0, true
	}
	return "", 0, true
}

// sweep drops windows older than the longest rule window. Callers hold mu.
func (l *Limiter) sweep(now time.Time) {
	var longest time.Duration
	for _, r := range l.rules {
		longest = max(longest, r.Window)
	}
	for k, w := range l.hits {
		if now.Sub(w.start) >= longest {
			delete(l.hits, k)
		}
	}
}

// Middleware answers 429 with Retry-After once a client exceeds a rule.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rule, wait, ok := l.Allow(r)
		if !ok {
			secs := int(wait.Round(time.Second) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
			l.log.Warn("rate limited", "rule", rule, "request_id", httpx.RequestID(r.Context()), "path", r.URL.Path)
			httpx.WriteProblem(w, httpx.Problem{Title: "Too many requests. Please wait a moment and try again.", Status: http.StatusTooManyRequests, Code: "rate_limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP returns the caller's address. X-Forwarded-For is only believed when the direct peer is a
// trusted proxy (the storefront), and then the right-most untrusted hop is used, so clients can't
// pick their own identity by sending the header themselves.
func ClientIP(trusted []*net.IPNet) func(*http.Request) string {
	isTrusted := func(ip net.IP) bool {
		for _, n := range trusted {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer := net.ParseIP(host)
		if peer == nil || !isTrusted(peer) {
			return host
		}
		hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip != nil && !isTrusted(ip) {
				return ip.String()
			}
		}
		return host
	}
}

// ParseCIDRs parses "127.0.0.1/32,::1/128"; bare IPs are accepted as single-host networks.
func ParseCIDRs(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// Path helpers for rules.

func Post(path string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPost && r.URL.Path == path }
}

func PostPrefix(prefix string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, prefix)
	}
}

// Writes matches any state-changing method under prefix.
func Writes(prefix string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && strings.HasPrefix(r.URL.Path, prefix)
	}
}
