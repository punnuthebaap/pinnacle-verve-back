package catalogadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// HTTPRevalidator asks the storefront to drop cached pages (Next.js revalidateTag), e.g. "catalog".
// Calls are fire-and-forget with a short retry; a failure is logged, never surfaced to the admin, and
// cached pages still expire on their own within minutes.
type HTTPRevalidator struct {
	URL    string // e.g. http://localhost:3000/internal/revalidate; empty disables
	Secret string
	Log    *slog.Logger
	Client *http.Client
}

func (r HTTPRevalidator) CatalogChanged() { r.Revalidate("catalog") }

// Revalidate asks the storefront to drop cached data with the given tags (its route allow-lists them).
func (r HTTPRevalidator) Revalidate(tags ...string) {
	if r.URL == "" {
		return
	}
	body, _ := json.Marshal(map[string][]string{"tags": tags})
	go func() {
		client := r.Client
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Second}
		}
		for attempt := 1; attempt <= 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Revalidate-Secret", r.Secret)
			res, err := client.Do(req)
			cancel()
			if err == nil {
				res.Body.Close()
				if res.StatusCode < 300 {
					return
				}
			}
			if attempt == 3 {
				r.Log.Warn("storefront revalidation failed", "url", r.URL, "err", err)
				return
			}
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}()
}
