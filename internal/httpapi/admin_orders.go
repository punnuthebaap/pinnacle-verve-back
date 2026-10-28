package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/orderadmin"
)

// orderProblem maps order-admin errors; ok=false means the error is unexpected (→ 500).
func orderProblem(err error) (api.Problem, bool) {
	var inv *orderadmin.InvalidError
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return pForbidden, true
	case errors.Is(err, orderadmin.ErrNotFound):
		return pNotFound, true
	case errors.As(err, &inv):
		return problem(422, "invalid", inv.Msg), true
	}
	return api.Problem{}, false
}

// orderResult returns the order after a read or write, or maps the error to the operation's problem response.
func orderResult[R any](d orderadmin.Detail, err error, ok200 func(api.AdminOrderJSONResponse) R, pick func(api.Problem) R) (R, error) {
	var zero R
	if err != nil {
		if p, ok := orderProblem(err); ok {
			return pick(p), nil
		}
		return zero, err
	}
	return ok200(api.AdminOrderJSONResponse(adminOrderJSON(d))), nil
}

func attentionJSON(reason string) *api.OrderAttention {
	if reason == "" {
		return nil
	}
	a := api.OrderAttention(reason)
	return &a
}

func adminOrderJSON(d orderadmin.Detail) api.AdminOrder {
	out := api.AdminOrder{Order: orderJSON(d.View), Attention: attentionJSON(d.Attention), Events: make([]api.AdminOrderEvent, 0, len(d.Events))}
	out.Payment.Provider = d.Payment.Provider
	out.Payment.ProviderOrderId = d.Payment.ProviderOrderID
	out.Payment.ProviderPaymentId = d.Payment.ProviderPaymentID
	out.Payment.Status = api.AdminOrderPaymentStatus(d.Payment.Status)
	out.Payment.AmountPaise = d.Payment.AmountPaise
	for _, e := range d.Events {
		data := map[string]any{}
		_ = json.Unmarshal(e.Data, &data)
		out.Events = append(out.Events, api.AdminOrderEvent{Id: e.ID, Type: e.Type, Data: data, CreatedAt: e.CreatedAt.Time, AdminName: nonEmpty(e.AdminName)})
	}
	return out
}

func (s *Server) AdminListOrders(ctx context.Context, r api.AdminListOrdersRequestObject) (api.AdminListOrdersResponseObject, error) {
	lq := orderadmin.ListQuery{Page: 1}
	if r.Params.Q != nil {
		lq.Q = *r.Params.Q
	}
	if r.Params.Queue != nil {
		lq.Queue = string(*r.Params.Queue)
	}
	if r.Params.Page != nil {
		lq.Page = *r.Params.Page
	}
	if v := r.Params.Status; v != nil {
		lq.Status = (*string)(v)
	}
	if v := r.Params.PaymentStatus; v != nil {
		lq.PaymentStatus = (*string)(v)
	}
	if v := r.Params.FulfilmentStatus; v != nil {
		lq.FulfilmentStatus = (*string)(v)
	}
	rows, total, counts, err := s.OrderAdmin.List(ctx, lq)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListOrders403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListOrders200JSONResponse{Page: lq.Page, Total: total, Items: make([]api.AdminOrderSummary, 0, len(rows))}
	out.Counts.ToFulfil, out.Counts.Attention = counts.ToFulfil, counts.Attention
	for _, o := range rows {
		out.Items = append(out.Items, api.AdminOrderSummary{
			Ref: o.Ref, CreatedAt: o.CreatedAt.Time, Email: o.Email, CustomerName: o.CustomerName, Phone: o.Phone, City: o.City,
			Status: api.AdminOrderSummaryStatus(o.Status), PaymentStatus: api.AdminOrderSummaryPaymentStatus(o.PaymentStatus),
			FulfilmentStatus: api.FulfilmentStatus(o.FulfilmentStatus), PaymentMethod: api.PaymentMethod(o.PaymentMethod),
			TotalPaise: o.TotalPaise, ItemCount: int(o.ItemCount), Attention: attentionJSON(o.Attention),
		})
	}
	return out, nil
}

func (s *Server) AdminGetOrder(ctx context.Context, r api.AdminGetOrderRequestObject) (api.AdminGetOrderResponseObject, error) {
	d, err := s.OrderAdmin.Get(ctx, r.Ref)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminGetOrderResponseObject {
			return api.AdminGetOrder200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminGetOrderResponseObject {
			if p.Status == 403 {
				return api.AdminGetOrder403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			}
			return api.AdminGetOrder404ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminSetFulfilment(ctx context.Context, r api.AdminSetFulfilmentRequestObject) (api.AdminSetFulfilmentResponseObject, error) {
	in := orderadmin.FulfilmentInput{Status: string(r.Body.Status)}
	if r.Body.Carrier != nil {
		in.Carrier = *r.Body.Carrier
	}
	if r.Body.TrackingNumber != nil {
		in.TrackingNumber = *r.Body.TrackingNumber
	}
	if r.Body.Note != nil {
		in.Note = *r.Body.Note
	}
	d, err := s.OrderAdmin.SetFulfilment(ctx, r.Ref, in)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminSetFulfilmentResponseObject {
			return api.AdminSetFulfilment200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminSetFulfilmentResponseObject {
			switch p.Status {
			case 403:
				return api.AdminSetFulfilment403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminSetFulfilment404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminSetFulfilment422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminCancelOrder(ctx context.Context, r api.AdminCancelOrderRequestObject) (api.AdminCancelOrderResponseObject, error) {
	d, err := s.OrderAdmin.Cancel(ctx, r.Ref, r.Body.Reason)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminCancelOrderResponseObject {
			return api.AdminCancelOrder200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminCancelOrderResponseObject {
			switch p.Status {
			case 403:
				return api.AdminCancelOrder403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminCancelOrder404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminCancelOrder422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminRecordRefund(ctx context.Context, r api.AdminRecordRefundRequestObject) (api.AdminRecordRefundResponseObject, error) {
	note := ""
	if r.Body.Note != nil {
		note = *r.Body.Note
	}
	d, err := s.OrderAdmin.RecordRefund(ctx, r.Ref, r.Body.Reference, note)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminRecordRefundResponseObject {
			return api.AdminRecordRefund200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminRecordRefundResponseObject {
			switch p.Status {
			case 403:
				return api.AdminRecordRefund403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminRecordRefund404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminRecordRefund422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminReinstateOrder(ctx context.Context, r api.AdminReinstateOrderRequestObject) (api.AdminReinstateOrderResponseObject, error) {
	d, err := s.OrderAdmin.Reinstate(ctx, r.Ref)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminReinstateOrderResponseObject {
			return api.AdminReinstateOrder200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminReinstateOrderResponseObject {
			switch p.Status {
			case 403:
				return api.AdminReinstateOrder403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminReinstateOrder404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminReinstateOrder422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminAddOrderNote(ctx context.Context, r api.AdminAddOrderNoteRequestObject) (api.AdminAddOrderNoteResponseObject, error) {
	d, err := s.OrderAdmin.AddNote(ctx, r.Ref, r.Body.Text)
	return orderResult(d, err,
		func(o api.AdminOrderJSONResponse) api.AdminAddOrderNoteResponseObject {
			return api.AdminAddOrderNote200JSONResponse{AdminOrderJSONResponse: o}
		},
		func(p api.Problem) api.AdminAddOrderNoteResponseObject {
			switch p.Status {
			case 403:
				return api.AdminAddOrderNote403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminAddOrderNote404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminAddOrderNote422ApplicationProblemPlusJSONResponse(p)
		})
}
