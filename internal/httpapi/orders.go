package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/payment"
)

func (s *Server) CreateOrder(ctx context.Context, r api.CreateOrderRequestObject) (api.CreateOrderResponseObject, error) {
	b := r.Body
	in := order.CreateInput{
		Email:         string(b.Email),
		PaymentMethod: string(b.PaymentMethod),
		Address: order.Address{
			FullName: strings.TrimSpace(b.ShippingAddress.FullName), Phone: b.ShippingAddress.Phone,
			Line1: strings.TrimSpace(b.ShippingAddress.Line1), City: strings.TrimSpace(b.ShippingAddress.City),
			State: strings.TrimSpace(b.ShippingAddress.State), Pincode: b.ShippingAddress.Pincode,
		},
	}
	if b.ShippingAddress.Line2 != nil {
		in.Address.Line2 = strings.TrimSpace(*b.ShippingAddress.Line2)
	}
	if b.Gstin != nil {
		in.GSTIN = *b.Gstin
	}
	if b.Notes != nil {
		in.Notes = strings.TrimSpace(*b.Notes)
	}
	v, err := s.Orders.Create(ctx, r.Params.IdempotencyKey, in)
	switch {
	case errors.Is(err, order.ErrEmptyCart):
		return api.CreateOrder422ApplicationProblemPlusJSONResponse(pEmptyCart), nil
	case errors.Is(err, order.ErrMethodUnavailable):
		return api.CreateOrder422ApplicationProblemPlusJSONResponse(pMethod), nil
	case errors.Is(err, order.ErrOutOfStock):
		return api.CreateOrder409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pOutOfStock)}, nil
	case errors.Is(err, order.ErrUnavailable):
		return api.CreateOrder409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pUnavailable)}, nil
	case errors.Is(err, order.ErrGatewayUnavailable):
		return api.CreateOrder503ApplicationProblemPlusJSONResponse(pGateway), nil
	case errors.Is(err, order.ErrDuplicateInFlight):
		return api.CreateOrder409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pDuplicate)}, nil
	case err != nil:
		return nil, err
	}
	return api.CreateOrder201JSONResponse{
		Order: orderJSON(v),
		Payment: api.PaymentSession{
			Provider: api.PaymentSessionProvider(v.Payment.Provider), ProviderOrderId: v.Payment.ProviderOrderID,
			AmountPaise: v.Payment.AmountPaise, Currency: api.INR,
		},
	}, nil
}

func (s *Server) GetOrder(ctx context.Context, r api.GetOrderRequestObject) (api.GetOrderResponseObject, error) {
	v, err := s.Orders.Get(ctx, r.Ref)
	if errors.Is(err, order.ErrNotFound) {
		return api.GetOrder404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetOrder200JSONResponse(orderJSON(v)), nil
}

func (s *Server) CompleteMockPayment(ctx context.Context, r api.CompleteMockPaymentRequestObject) (api.CompleteMockPaymentResponseObject, error) {
	err := s.Orders.CompleteMock(ctx, r.ProviderOrderId, r.Body.Outcome == api.Success)
	if errors.Is(err, order.ErrNotFound) || errors.Is(err, order.ErrMockNotEnabled) {
		return api.CompleteMockPayment404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.CompleteMockPayment204Response{}, nil
}

func (s *Server) ReceivePaymentWebhook(ctx context.Context, r api.ReceivePaymentWebhookRequestObject) (api.ReceivePaymentWebhookResponseObject, error) {
	raw, _ := ctx.Value(rawBodyKey{}).([]byte)
	err := s.Orders.HandleWebhook(ctx, string(r.Provider), headersFrom(ctx), raw)
	if errors.Is(err, payment.ErrBadSignature) || errors.Is(err, order.ErrUnknownProvider) {
		return api.ReceivePaymentWebhook401ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pBadSignature)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.ReceivePaymentWebhook200Response{}, nil
}

type rawBodyKey struct{}
type headersKey struct{}

func headersFrom(ctx context.Context) http.Header {
	h, _ := ctx.Value(headersKey{}).(http.Header)
	return h
}

// CaptureWebhook keeps the exact webhook bytes and headers for signature verification, since the strict
// handler only exposes the decoded body.
func CaptureWebhook(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/payments/webhooks/") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			ctx := context.WithValue(r.Context(), rawBodyKey{}, body)
			r = r.WithContext(context.WithValue(ctx, headersKey{}, r.Header.Clone()))
		}
		next.ServeHTTP(w, r)
	})
}

func orderJSON(v order.View) api.Order {
	o := api.Order{
		Ref: v.Ref, Status: api.OrderStatus(v.Status), PaymentStatus: api.OrderPaymentStatus(v.PaymentStatus),
		FulfilmentStatus: api.FulfilmentStatus(v.FulfilmentStatus), CreatedAt: v.CreatedAt.Time,
		Email: v.Email, Gstin: nonEmpty(v.Gstin), Notes: nonEmpty(v.Notes),
		PaymentMethod: api.PaymentMethod(v.PaymentMethod),
		ShippingAddress: api.Address{
			FullName: v.Address.FullName, Phone: v.Address.Phone, Line1: v.Address.Line1, Line2: nonEmpty(v.Address.Line2),
			City: v.Address.City, State: v.Address.State, Pincode: v.Address.Pincode,
		},
		Totals: totalsJSON(v.SubtotalPaise, v.DiscountPaise, v.ShippingPaise, v.AddonsPaise, v.TaxPaise, v.TotalPaise),
		Items:  make([]api.OrderItem, 0, len(v.Items)), Addons: make([]api.Addon, 0, len(v.Addons)),
	}
	if v.TrackingCarrier != "" || v.TrackingNumber != "" {
		o.Tracking = &api.Tracking{Carrier: v.TrackingCarrier, Number: v.TrackingNumber}
	}
	for _, i := range v.Items {
		o.Items = append(o.Items, api.OrderItem{
			ProductSlug: i.ProductSlug, ProductName: i.ProductName, VariantLabel: i.VariantLabel, Sku: i.Sku,
			Image: imageOf(i.ImageUrl, i.ImageAlt), UnitPricePaise: i.UnitPricePaise,
			Quantity: int(i.Quantity), LineTotalPaise: i.LineTotalPaise,
		})
	}
	for _, a := range v.Addons {
		o.Addons = append(o.Addons, api.Addon{Code: a.Code, Name: a.Name, Description: a.Description, PricePaise: a.PricePaise})
	}
	return o
}
