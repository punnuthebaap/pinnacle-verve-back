// Package httpapi implements the generated OpenAPI StrictServerInterface. Handlers only translate between
// transport types (package api) and services; business rules live in the service packages.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/cart"
	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/catalogadmin"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/orderadmin"
	"github.com/pinnacleverve/api/internal/platform/httpx"
	"github.com/pinnacleverve/api/internal/settings"
	"github.com/pinnacleverve/api/internal/store"
)

type Server struct {
	Pool         *pgxpool.Pool
	Q            *store.Queries
	Catalog      *catalog.Service
	Cart         *cart.Service
	Orders       *order.Service
	Admin        *admin.Service
	CatalogAdmin *catalogadmin.Service
	OrderAdmin   *orderadmin.Service
	Settings     *settings.Service
	Log          *slog.Logger
}

var _ api.StrictServerInterface = (*Server)(nil)

// Handler wires the strict server with problem-details error handling.
func (s *Server) Handler(mux *http.ServeMux) http.Handler {
	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			httpx.WriteProblem(w, httpx.Problem{Title: "Malformed request", Status: 400, Code: "bad_request", Detail: err.Error()})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.Error("handler error", "request_id", httpx.RequestID(r.Context()), "err", err)
			httpx.WriteProblem(w, httpx.Problem{Title: "Internal error", Status: 500, Code: "internal"})
		},
	})
	return api.HandlerWithOptions(strict, api.StdHTTPServerOptions{BaseURL: "/api/v1", BaseRouter: mux})
}

func problem(status int, code, title string) api.Problem {
	return api.Problem{Status: status, Code: code, Title: title}
}

// Stable error codes shared with the frontend.
var (
	pNotFound      = problem(404, "not_found", "Not found")
	pOutOfStock    = problem(409, "out_of_stock", "Not enough stock for the requested quantity")
	pLineLimit     = problem(422, "quantity_limit", "Maximum quantity per item reached")
	pUnknownAddon  = problem(422, "unknown_addon", "One or more add-ons are unavailable")
	pEmptyCart     = problem(422, "empty_cart", "Your bag is empty")
	pUnavailable   = problem(409, "item_unavailable", "An item in your bag is no longer available")
	pMethod        = problem(422, "payment_method_unavailable", "This payment method is not available yet")
	pDuplicate     = problem(409, "duplicate_request", "This order is already being processed")
	pBadSignature  = problem(401, "invalid_signature", "Invalid webhook signature")
	pUnavailableDB = problem(503, "unavailable", "Service unavailable")
)

func (s *Server) GetHealth(ctx context.Context, _ api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	if err := s.Pool.Ping(ctx); err != nil {
		return api.GetHealth503ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pUnavailableDB)}, nil
	}
	return api.GetHealth200JSONResponse{Status: api.Ok}, nil
}

func (s *Server) SubscribeNewsletter(ctx context.Context, r api.SubscribeNewsletterRequestObject) (api.SubscribeNewsletterResponseObject, error) {
	if err := s.Q.InsertNewsletterSubscriber(ctx, string(r.Body.Email)); err != nil {
		return nil, err
	}
	return api.SubscribeNewsletter202Response{}, nil
}
