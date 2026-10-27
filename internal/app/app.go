// Package app assembles services and the HTTP middleware stack.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/jackc/pgx/v5/pgxpool"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/cart"
	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/httpapi"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/payment"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/httpx"
	"github.com/pinnacleverve/api/internal/platform/session"
	"github.com/pinnacleverve/api/internal/store"
)

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
	srv := &httpapi.Server{
		Pool:    pool,
		Q:       q,
		Catalog: catalog.NewService(q),
		Cart:    cart.NewService(pool, q, cfg),
		Orders:  order.NewService(pool, q, cfg, provider, log),
		Admin:   admin.NewService(q, log),
		Log:     log,
	}

	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		SilenceServersWarning: true,
		Options:               openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
			code := "validation_failed"
			if opts.StatusCode == http.StatusNotFound {
				code = "not_found"
			}
			httpx.WriteProblem(w, httpx.Problem{Title: "Request validation failed", Status: opts.StatusCode, Code: code, Detail: err.Error()})
		},
	})

	handler := httpx.Chain(srv.Handler(http.NewServeMux()),
		httpx.Observe(log),
		httpx.Harden(1<<20),
		httpx.SameOrigin(cfg.AllowedOrigins),
		session.Middleware(cfg.CookieSecure, cart.Persister(q)),
		srv.Admin.Middleware(cfg.CookieSecure),
		httpapi.CaptureWebhook,
		validate,
	)
	return &App{Handler: handler, Orders: srv.Orders}, nil
}
