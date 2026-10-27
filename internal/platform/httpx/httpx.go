// Package httpx holds HTTP middleware and RFC 9457 problem responses shared by all handlers.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

type Problem struct {
	Type   string       `json:"type,omitempty"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Code   string       `json:"code"`
	Errors []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func WriteProblem(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

type ctxKey int

const requestIDKey ctxKey = iota

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Observe assigns a request ID, recovers panics and logs one line per request.
func Observe(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" || len(id) > 64 {
				b := make([]byte, 8)
				_, _ = rand.Read(b)
				id = hex.EncodeToString(b)
			}
			w.Header().Set("X-Request-Id", id)
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			defer func() {
				if p := recover(); p != nil {
					log.Error("panic", "request_id", id, "panic", p)
					WriteProblem(rec, Problem{Title: "Internal error", Status: 500, Code: "internal"})
				}
				log.Info("http", "request_id", id, "method", r.Method, "path", r.URL.Path,
					"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

// Harden sets baseline security headers and caps request bodies.
func Harden(maxBody int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
			next.ServeHTTP(w, r)
		})
	}
}

// SameOrigin rejects state-changing browser requests from origins not in the allow-list (CSRF defence).
// Requests without an Origin header (server-to-server, gateway webhooks) are allowed; webhooks are
// authenticated by signature instead.
func SameOrigin(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
			default:
				if o := r.Header.Get("Origin"); o != "" && !slices.Contains(allowed, o) {
					WriteProblem(w, Problem{Title: "Forbidden origin", Status: 403, Code: "forbidden_origin"})
					return
				}
				if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					WriteProblem(w, Problem{Title: "Cross-site request rejected", Status: 403, Code: "forbidden_origin"})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
