// Package orderadmin implements order operations for the admin panel (phase A3): search, detail with
// timeline, fulfilment updates, cancellation, refund records, late-capture resolution and internal notes.
//
// Every change locks the order row, writes an order event (with the acting admin) and an audit entry in
// the same transaction. Refunds are issued in the gateway dashboard and only recorded here until the
// gateway integration exists (ROADMAP B1); only full refunds are modelled.
package orderadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/order"
	"github.com/pinnacleverve/api/internal/payment"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

const OrdersPerPage = 25

var ErrNotFound = errors.New("orderadmin: not found")

// InvalidError is a business-rule violation with a message an admin can act on.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

// FulfilmentSteps is the forward-only fulfilment sequence.
var FulfilmentSteps = []string{"unfulfilled", "preparing", "in_transit", "delivered", "installed"}

type Service struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	audit *admin.Service
}

func NewService(pool *pgxpool.Pool, q *store.Queries, audit *admin.Service) *Service {
	return &Service{pool: pool, q: q, audit: audit}
}

func authorize(ctx context.Context) (store.AdminUser, error) {
	me := admin.Current(ctx)
	if !admin.Can(me.Role, admin.AreaOrders) {
		return me, admin.ErrForbidden
	}
	return me, nil
}

// ---- reads ----

type ListQuery struct {
	Q, Queue                                string
	Status, PaymentStatus, FulfilmentStatus *string
	Page                                    int
}

type Counts struct{ ToFulfil, Attention int }

func (s *Service) List(ctx context.Context, lq ListQuery) ([]store.AdminListOrdersRow, int, Counts, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, 0, Counts{}, err
	}
	text := func(p *string) pgtype.Text {
		if p == nil {
			return pgtype.Text{}
		}
		return pgtype.Text{String: *p, Valid: true}
	}
	rows, err := s.q.AdminListOrders(ctx, store.AdminListOrdersParams{
		Q: strings.TrimSpace(lq.Q), Queue: lq.Queue, Status: text(lq.Status), PaymentStatus: text(lq.PaymentStatus),
		FulfilmentStatus: text(lq.FulfilmentStatus), Lim: OrdersPerPage, Off: int32((lq.Page - 1) * OrdersPerPage),
	})
	if err != nil {
		return nil, 0, Counts{}, err
	}
	c, err := s.q.AdminOrderCounts(ctx)
	if err != nil {
		return nil, 0, Counts{}, err
	}
	total := 0
	if len(rows) > 0 {
		total = int(rows[0].TotalCount)
	}
	return rows, total, Counts{ToFulfil: int(c.ToFulfil), Attention: int(c.Attention)}, nil
}

type Detail struct {
	order.View
	Attention string
	Events    []store.AdminListOrderEventsRow
}

func (s *Service) Get(ctx context.Context, ref string) (Detail, error) {
	if _, err := authorize(ctx); err != nil {
		return Detail{}, err
	}
	id, err := s.q.AdminGetOrderIDByRef(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	return s.detail(ctx, s.q, id)
}

func (s *Service) detail(ctx context.Context, q *store.Queries, id uuid.UUID) (Detail, error) {
	v, err := order.Load(ctx, q, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{View: v}
	if d.Attention, err = q.AdminOrderAttention(ctx, id); err != nil {
		return d, err
	}
	d.Events, err = q.AdminListOrderEvents(ctx, id)
	return d, err
}

// ---- writes ----

// change locks the order, lets fn apply the change, then records the order event and audit entry.
func (s *Service) change(ctx context.Context, ref, action string, fn func(q *store.Queries, o store.Order) (event string, data map[string]any, err error)) (Detail, error) {
	me, err := authorize(ctx)
	if err != nil {
		return Detail{}, err
	}
	id, err := s.q.AdminGetOrderIDByRef(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	var d Detail
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// Lock order: orders before payments, the same as the expiry sweeper.
		o, err := q.AdminLockOrder(ctx, id)
		if err != nil {
			return err
		}
		typ, data, err := fn(q, o)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(data)
		if err := q.AdminInsertOrderEvent(ctx, store.AdminInsertOrderEventParams{
			OrderID: id, Type: typ, Data: b, AdminID: pgtype.UUID{Bytes: me.ID, Valid: true},
		}); err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, action, "order", ref, data)
		d, err = s.detail(ctx, q, id)
		return err
	})
	return d, err
}

type FulfilmentInput struct {
	Status, Carrier, TrackingNumber, Note string
}

// SetFulfilment moves a paid order forward (steps may be skipped, never reversed). Tracking details can
// be added or corrected while the order is in transit.
func (s *Service) SetFulfilment(ctx context.Context, ref string, in FulfilmentInput) (Detail, error) {
	to := slices.Index(FulfilmentSteps, in.Status)
	if to <= 0 {
		return Detail{}, invalid("Unknown fulfilment status %q.", in.Status)
	}
	return s.change(ctx, ref, "order.fulfilment", func(q *store.Queries, o store.Order) (string, map[string]any, error) {
		if o.Status != "placed" {
			return "", nil, invalid("Only paid orders can be fulfilled (this one is %s).", strings.ReplaceAll(o.Status, "_", " "))
		}
		from := slices.Index(FulfilmentSteps, o.FulfilmentStatus)
		carrier, number := strings.TrimSpace(in.Carrier), strings.TrimSpace(in.TrackingNumber)
		if carrier == "" && number == "" {
			carrier, number = o.TrackingCarrier, o.TrackingNumber // keep what was entered before
		}
		trackingChanged := carrier != o.TrackingCarrier || number != o.TrackingNumber
		switch {
		case to < from:
			return "", nil, invalid("Fulfilment only moves forward; the order is already %s.", label(o.FulfilmentStatus))
		case to == from && !trackingChanged:
			return "", nil, invalid("The order is already %s.", label(o.FulfilmentStatus))
		case trackingChanged && to < slices.Index(FulfilmentSteps, "in_transit"):
			return "", nil, invalid("Add tracking details when the order is dispatched (in transit).")
		}
		if err := q.AdminSetFulfilment(ctx, store.AdminSetFulfilmentParams{ID: o.ID, FulfilmentStatus: in.Status, TrackingCarrier: carrier, TrackingNumber: number}); err != nil {
			return "", nil, err
		}
		data := map[string]any{"from": o.FulfilmentStatus, "to": in.Status}
		if carrier != "" || number != "" {
			data["carrier"], data["trackingNumber"] = carrier, number
		}
		if note := strings.TrimSpace(in.Note); note != "" {
			data["note"] = note
		}
		if to == from {
			return "tracking_updated", data, nil
		}
		return "fulfilment_" + in.Status, data, nil
	})
}

// Cancel cancels an order that hasn't been dispatched and releases its stock. A paid order then needs a
// refund (it shows as "refund due" until one is recorded). An unpaid order's gateway order is closed so a
// payment that still arrives is flagged as a late capture instead of being accepted.
func (s *Service) Cancel(ctx context.Context, ref, reason string) (Detail, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return Detail{}, invalid("Give a reason for the cancellation.")
	}
	return s.change(ctx, ref, "order.cancel", func(q *store.Queries, o store.Order) (string, map[string]any, error) {
		switch {
		case o.Status == "cancelled":
			return "", nil, invalid("The order is already cancelled.")
		case slices.Index(FulfilmentSteps, o.FulfilmentStatus) >= slices.Index(FulfilmentSteps, "in_transit"):
			return "", nil, invalid("The order has been dispatched, so it can't be cancelled here; handle it as a return.")
		}
		if err := q.ReleaseOrderStock(ctx, o.ID); err != nil {
			return "", nil, err
		}
		paymentStatus := o.PaymentStatus
		if o.Status == "pending_payment" {
			if err := q.ExpireOrderPayments(ctx, o.ID); err != nil {
				return "", nil, err
			}
			paymentStatus = "expired"
		}
		if err := q.UpdateOrderPaymentState(ctx, store.UpdateOrderPaymentStateParams{ID: o.ID, Status: "cancelled", PaymentStatus: paymentStatus}); err != nil {
			return "", nil, err
		}
		return "order_cancelled", map[string]any{"reason": reason, "previousStatus": o.Status}, nil
	})
}

// RecordRefund records a full refund issued in the gateway dashboard for an order that needs one.
func (s *Service) RecordRefund(ctx context.Context, ref, reference, note string) (Detail, error) {
	reference = strings.TrimSpace(reference)
	if len(reference) < 3 {
		return Detail{}, invalid("Enter the gateway's refund reference.")
	}
	return s.change(ctx, ref, "order.refund_recorded", func(q *store.Queries, o store.Order) (string, map[string]any, error) {
		reason, err := q.AdminOrderAttention(ctx, o.ID)
		if err != nil {
			return "", nil, err
		}
		if reason == "" {
			return "", nil, invalid("This order has no payment to refund. Cancel it first if the customer is owed money.")
		}
		if err := q.AdminSetPaymentStatus(ctx, store.AdminSetPaymentStatusParams{ID: o.ID, PaymentStatus: "refunded"}); err != nil {
			return "", nil, err
		}
		data := map[string]any{"reference": reference, "reason": reason}
		if note = strings.TrimSpace(note); note != "" {
			data["note"] = note
		}
		return "refund_recorded", data, nil
	})
}

// Reinstate accepts a payment that arrived after the order expired: stock is reserved again (all or
// nothing) and the order is placed as if the payment had been on time.
func (s *Service) Reinstate(ctx context.Context, ref string) (Detail, error) {
	return s.change(ctx, ref, "order.reinstate", func(q *store.Queries, o store.Order) (string, map[string]any, error) {
		reason, err := q.AdminOrderAttention(ctx, o.ID)
		if err != nil {
			return "", nil, err
		}
		if reason != "late_capture" {
			return "", nil, invalid("Only orders paid after they expired can be reinstated.")
		}
		raw, err := q.AdminLateCaptureEvent(ctx, o.ID)
		if err != nil {
			return "", nil, err
		}
		var ev payment.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return "", nil, err
		}
		if ev.AmountPaise != o.TotalPaise {
			return "", nil, invalid("The captured amount doesn't match the order total; refund it instead.")
		}
		items, err := q.ListOrderItems(ctx, o.ID)
		if err != nil {
			return "", nil, err
		}
		for _, it := range items {
			n, err := q.ReserveStock(ctx, store.ReserveStockParams{ID: it.VariantID, Qty: it.Quantity})
			if err != nil {
				return "", nil, err
			}
			if n == 0 {
				return "", nil, invalid("Not enough stock of %s to reinstate the order; record a refund instead.", it.Sku)
			}
		}
		pay, err := q.GetPaymentByOrderID(ctx, o.ID)
		if err != nil {
			return "", nil, err
		}
		if err := q.UpdatePaymentStatus(ctx, store.UpdatePaymentStatusParams{ID: pay.ID, Status: "captured", ProviderPaymentID: ev.ProviderPaymentID}); err != nil {
			return "", nil, err
		}
		if err := q.UpdateOrderPaymentState(ctx, store.UpdateOrderPaymentStateParams{ID: o.ID, Status: "placed", PaymentStatus: "paid"}); err != nil {
			return "", nil, err
		}
		return "order_reinstated", map[string]any{"providerPaymentId": ev.ProviderPaymentID}, nil
	})
}

// AddNote adds an internal note to the timeline (never shown to the customer).
func (s *Service) AddNote(ctx context.Context, ref, text string) (Detail, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 {
		return Detail{}, invalid("Write a note first.")
	}
	return s.change(ctx, ref, "order.note", func(_ *store.Queries, _ store.Order) (string, map[string]any, error) {
		return "note", map[string]any{"text": text}, nil
	})
}

func label(status string) string { return strings.ReplaceAll(status, "_", " ") }
