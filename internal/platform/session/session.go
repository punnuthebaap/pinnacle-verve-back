// Package session manages the anonymous browser session that owns carts and guest orders.
//
// The cookie holds a random 256-bit token; only its SHA-256 is stored server-side. A session row is
// created lazily, the first time a handler calls Ensure (e.g. adding to cart), so catalogue browsing
// never writes to the database or sets cookies.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"
)

const (
	CookieName = "pv_sid"
	TTL        = 30 * 24 * time.Hour
)

// Persister stores (or refreshes) a session row.
type Persister func(ctx context.Context, id []byte, expiresAt time.Time) error

type state struct {
	w       http.ResponseWriter
	id      []byte // SHA-256 of token; nil when the request carried no valid cookie
	secure  bool
	persist Persister
	ensured bool
}

type ctxKey struct{}

func Middleware(secure bool, persist Persister) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := &state{w: w, secure: secure, persist: persist}
			if c, err := r.Cookie(CookieName); err == nil {
				if raw, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil && len(raw) == 32 {
					st.id = hash(raw)
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, st)))
		})
	}
}

// ID returns the current session id, or nil if the request has no session.
func ID(ctx context.Context) []byte {
	if st, ok := ctx.Value(ctxKey{}).(*state); ok {
		return st.id
	}
	return nil
}

// Ensure returns a persisted session id, issuing a new cookie if needed. Call before writing the response.
func Ensure(ctx context.Context) ([]byte, error) {
	st, ok := ctx.Value(ctxKey{}).(*state)
	if !ok {
		panic("session: middleware not installed")
	}
	if st.ensured {
		return st.id, nil
	}
	if st.id == nil {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		st.id = hash(raw)
		http.SetCookie(st.w, &http.Cookie{
			Name:     CookieName,
			Value:    base64.RawURLEncoding.EncodeToString(raw),
			Path:     "/",
			MaxAge:   int(TTL.Seconds()),
			HttpOnly: true,
			Secure:   st.secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	if err := st.persist(ctx, st.id, time.Now().Add(TTL)); err != nil {
		return nil, err
	}
	st.ensured = true
	return st.id, nil
}

func hash(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}
