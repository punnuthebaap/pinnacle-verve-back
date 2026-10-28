// Package app assembles services and the HTTP middleware stack.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/jackc/pgx/v5/pgxpool"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/cart"
	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/catalogadmin"
	"github.com/pinnacleverve/api/internal/content"
	"github.com/pinnacleverve/api/internal/httpapi"
	"github.com/pinnacleverve/api/internal/marketing"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/orderadmin"
	"github.com/pinnacleverve/api/internal/payment"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/httpx"
	"github.com/pinnacleverve/api/internal/platform/ratelimit"
	"github.com/pinnacleverve/api/internal/platform/session"
	"github.com/pinnacleverve/api/internal/settings"
	"github.com/pinnacleverve/api/internal/storage"
	"github.com/pinnacleverve/api/internal/store"
)

// bodyLimit caps request bodies at 1 MiB, except image uploads (10 MiB + headroom).
func bodyLimit(r *http.Request) int64 {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/admin/products/") && strings.HasSuffix(r.URL.Path, "/images") {
		return catalogadmin.MaxImageBytes + 1<<10
	}
	return 1 << 20
}

// rateRules are per client IP and per minute; the first matching rule applies. Sign-in also locks an
// account after repeated failures (package admin). Gateway webhooks are never limited.
var rateRules = []ratelimit.Rule{
	{Name: "admin_signin", Limit: 20, Window: time.Minute, Match: ratelimit.Post("/api/v1/admin/session")},
	{Name: "newsletter", Limit: 10, Window: time.Minute, Match: ratelimit.Post("/api/v1/newsletter/subscriptions")},
	{Name: "orders", Limit: 20, Window: time.Minute, Match: ratelimit.Post("/api/v1/orders")},
	{Name: "mock_payments", Limit: 30, Window: time.Minute, Match: ratelimit.PostPrefix("/api/v1/payments/mock/")},
	{Name: "cart", Limit: 120, Window: time.Minute, Match: ratelimit.Writes("/api/v1/cart")},
	{Name: "admin_uploads", Limit: 120, Window: time.Minute, Match: func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/admin/products/") && strings.HasSuffix(r.URL.Path, "/images")
	}},
}

// App is the assembled service: the HTTP handler plus background work.
type App struct {
	Handler http.Handler
	Orders  *order.Service
}

// NewHandler returns just the HTTP handler (kept for callers that need no background work).
func NewHandler(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error) {
	a, err := New(cfg, log, pool)
	if err != nil {
		return nil, err
	}
	return a.Handler, nil
}

// New assembles services and the middleware stack. Shared with integration tests.
func New(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (*App, error) {
	var provider payment.Provider
	switch cfg.PaymentProvider {
	case "mock":
		provider = payment.Mock{Secret: []byte(cfg.MockWebhookSecret)}
	default:
		return nil, fmt.Errorf("payment provider %q not implemented yet (ROADMAP B1)", cfg.PaymentProvider)
	}

	q := store.New(pool)
	adm := admin.NewService(q, log)
	rv := catalogadmin.HTTPRevalidator{URL: cfg.StorefrontRevalidateURL, Secret: cfg.RevalidateSecret, Log: log}
	st := settings.NewService(pool, q, adm, rv, settings.Defaults(cfg))
	srv := &httpapi.Server{
		Pool:     pool,
		Q:        q,
		Catalog:  catalog.NewService(q),
		Cart:     cart.NewService(pool, q, st),
		Orders:   order.NewService(pool, q, st, provider, log),
		Admin:    adm,
		Settings: st,
		Log:      log,
	}
	srv.OrderAdmin = orderadmin.NewService(pool, q, srv.Admin)
	srv.Marketing = marketing.NewService(pool, q, srv.Admin, rv)
	srv.Content = content.NewService(pool, q, srv.Admin, rv)
	media := storage.Local{Dir: cfg.MediaDir, URLPrefix: "/media"}
	srv.CatalogAdmin = catalogadmin.NewService(pool, q, srv.Admin, rv, media)

	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		SilenceServersWarning: true,
		Options:               openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
			// The validator reads the body first; hitting the size cap surfaces here (sometimes wrapped as
			// a "security requirements" failure), so report it as what it is.
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "request body too large") {
				httpx.WriteProblem(w, httpx.Problem{Title: "Request body too large", Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large"})
				return
			}
			code := "validation_failed"
			if opts.StatusCode == http.StatusNotFound {
				code = "not_found"
			}
			httpx.WriteProblem(w, httpx.Problem{Title: "Request validation failed", Status: opts.StatusCode, Code: code, Detail: err.Error()})
		},
	})

	trusted, err := ratelimit.ParseCIDRs(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	limits := func(next http.Handler) http.Handler { return next }
	if cfg.RateLimitEnabled {
		limits = ratelimit.New(rateRules, ratelimit.ClientIP(trusted), log).Middleware
	}

	handler := httpx.Chain(srv.Handler(http.NewServeMux()),
		httpx.Observe(log),
		httpx.Harden(bodyLimit),
		limits,
		httpx.SameOrigin(cfg.AllowedOrigins),
		session.Middleware(cfg.CookieSecure, cart.Persister(q)),
		srv.Admin.Middleware(cfg.CookieSecure),
		httpapi.CaptureWebhook,
		validate,
	)
	// /media serves uploaded files (proxied by the storefront); everything else is the API.
	root := http.NewServeMux()
	root.Handle("/media/", httpx.Chain(media.Handler(), httpx.Observe(log)))
	root.Handle("/", handler)
	return &App{Handler: root, Orders: srv.Orders}, nil
}
