// Package order converts carts into orders and applies payment outcomes from gateway webhooks.
//
// Lifecycle: create → status=pending_payment, payment=pending, stock reserved.
// Captured webhook → placed/paid, cart cleared. Failed webhook → cancelled/failed, stock released.
// Fulfilment status advances independently (operations, M4).
package order

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/cart"
	"github.com/pinnacleverve/api/internal/payment"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/platform/session"
	"github.com/pinnacleverve/api/internal/store"
)

var (
	ErrNotFound           = errors.New("order: not found")
	ErrEmptyCart          = errors.New("order: cart is empty")
	ErrUnavailable        = errors.New("order: an item is no longer available")
	ErrOutOfStock         = errors.New("order: insufficient stock")
	ErrMethodUnavailable  = errors.New("order: payment method unavailable")
	ErrDuplicateInFlight  = errors.New("order: duplicate request in progress")
	ErrUnknownProvider    = errors.New("order: unknown payment provider")
	ErrMockNotEnabled     = errors.New("order: mock payments disabled")
)

type Address struct {
	FullName string `json:"fullName"`
	Phone    string `json:"phone"`
	Line1    string `json:"line1"`
	Line2    string `json:"line2,omitempty"`
	City     string `json:"city"`
	State    string `json:"state"`
	Pincode  string `json:"pincode"`
}

type CreateInput struct {
	Email         string
	Address       Address
	GSTIN         string
	Notes         string
	PaymentMethod string
}

type View struct {
	store.Order
	Address Address
	Items   []store.OrderItem
	Addons  []store.OrderAddon
	Payment store.Payment
}

type Service struct {
	pool      *pgxpool.Pool
	q         *store.Queries
	cfg       config.Config
	active    payment.Provider
	providers map[string]payment.Provider
	log       *slog.Logger
}

func NewService(pool *pgxpool.Pool, q *store.Queries, cfg config.Config, active payment.Provider, log *slog.Logger, all ...payment.Provider) *Service {
	m := map[string]payment.Provider{active.Name(): active}
	for _, p := range all {
		m[p.Name()] = p
	}
	return &Service{pool: pool, q: q, cfg: cfg, active: active, providers: m, log: log}
}

func (s *Service) Create(ctx context.Context, idemKey string, in CreateInput) (View, error) {
	if !slices.Contains(s.cfg.EnabledPaymentMethods, in.PaymentMethod) {
		return View{}, ErrMethodUnavailable
	}
	sid, err := session.Ensure(ctx)
	if err != nil {
		return View{}, err
	}
	var orderID uuid.UUID
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, err := q.GetOrderIDByIdempotencyKey(ctx, store.GetOrderIDByIdempotencyKeyParams{SessionID: sid, IdempotencyKey: idemKey})
		if err == nil {
			orderID = id // replay: return the original order
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		orderID, err = s.create(ctx, q, sid, idemKey, in)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "orders_session_id_idempotency_key_key" {
		return View{}, ErrDuplicateInFlight
	}
	if err != nil {
		return View{}, err
	}
	return s.load(ctx, s.q, orderID)
}

func (s *Service) create(ctx context.Context, q *store.Queries, sid []byte, idemKey string, in CreateInput) (uuid.UUID, error) {
	cartRow, err := q.GetCartBySession(ctx, sid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrEmptyCart
	}
	if err != nil {
		return uuid.Nil, err
	}
	c, err := cart.Load(ctx, q, cartRow, s.cfg)
	if err != nil {
		return uuid.Nil, err
	}
	if in.Notes == "" {
		in.Notes = c.Notes // instructions entered on the cart page
	}
	if len(c.Lines) == 0 {
		return uuid.Nil, ErrEmptyCart
	}
	for _, l := range c.Lines {
		if !l.Available.Bool {
			return uuid.Nil, ErrUnavailable
		}
		n, err := q.ReserveStock(ctx, store.ReserveStockParams{ID: l.VariantID, Qty: l.Quantity})
		if err != nil {
			return uuid.Nil, err
		}
		if n == 0 {
			return uuid.Nil, fmt.Errorf("%w: %s", ErrOutOfStock, l.Sku)
		}
	}

	addr, _ := json.Marshal(in.Address)
	ref := newRef()
	orderID, err := q.InsertOrder(ctx, store.InsertOrderParams{
		Ref: ref, SessionID: sid, IdempotencyKey: idemKey, Email: in.Email, ShippingAddress: addr,
		Gstin: in.GSTIN, Notes: in.Notes, PaymentMethod: in.PaymentMethod,
		SubtotalPaise: c.Totals.SubtotalPaise, DiscountPaise: c.Totals.DiscountPaise,
		ShippingPaise: c.Totals.ShippingPaise, AddonsPaise: c.Totals.AddonsPaise,
		TaxPaise: c.Totals.TaxIncludedPaise, TotalPaise: c.Totals.TotalPaise,
	})
	if err != nil {
		return uuid.Nil, err
	}
	for _, l := range c.Lines {
		if err := q.InsertOrderItem(ctx, store.InsertOrderItemParams{
			OrderID: orderID, VariantID: l.VariantID, ProductSlug: l.ProductSlug, ProductName: l.ProductName,
			VariantLabel: l.VariantLabel, Sku: l.Sku, ImageUrl: l.ImageUrl, ImageAlt: l.ImageAlt,
			UnitPricePaise: l.PricePaise, Quantity: l.Quantity, LineTotalPaise: l.LineTotal,
		}); err != nil {
			return uuid.Nil, err
		}
	}
	for _, a := range c.Addons {
		if err := q.InsertOrderAddon(ctx, store.InsertOrderAddonParams{
			OrderID: orderID, Code: a.Code, Name: a.Name, Description: a.Description, PricePaise: a.PricePaise,
		}); err != nil {
			return uuid.Nil, err
		}
	}
	// NOTE: the gateway call happens inside the transaction for atomicity. With a real gateway, move it
	// after commit (with retry) to avoid holding stock row locks during network I/O — tracked in HANDOFF.
	ps, err := s.active.CreateOrder(ctx, ref, c.Totals.TotalPaise)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create gateway order: %w", err)
	}
	if err := q.InsertPayment(ctx, store.InsertPaymentParams{
		OrderID: orderID, Provider: ps.Provider, ProviderOrderID: ps.ProviderOrderID, AmountPaise: c.Totals.TotalPaise,
	}); err != nil {
		return uuid.Nil, err
	}
	return orderID, event(ctx, q, orderID, "order_created", map[string]any{"total_paise": c.Totals.TotalPaise})
}

func (s *Service) Get(ctx context.Context, ref string) (View, error) {
	sid := session.ID(ctx)
	if sid == nil {
		return View{}, ErrNotFound
	}
	o, err := s.q.GetOrderByRefForSession(ctx, store.GetOrderByRefForSessionParams{Ref: ref, SessionID: sid})
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	return s.load(ctx, s.q, o.ID)
}

func (s *Service) load(ctx context.Context, q *store.Queries, id uuid.UUID) (View, error) {
	o, err := q.GetOrderByID(ctx, id)
	if err != nil {
		return View{}, err
	}
	v := View{Order: o}
	if err := json.Unmarshal(o.ShippingAddress, &v.Address); err != nil {
		return v, err
	}
	if v.Items, err = q.ListOrderItems(ctx, id); err != nil {
		return v, err
	}
	if v.Addons, err = q.ListOrderAddons(ctx, id); err != nil {
		return v, err
	}
	v.Payment, err = q.GetPaymentByOrderID(ctx, id)
	return v, err
}

// HandleWebhook authenticates and applies a gateway event exactly once.
func (s *Service) HandleWebhook(ctx context.Context, provider string, h http.Header, body []byte) error {
	p, ok := s.providers[provider]
	if !ok {
		return ErrUnknownProvider
	}
	ev, err := p.ParseWebhook(h, body)
	if err != nil {
		return err
	}
	return database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		fresh, err := q.InsertPaymentEvent(ctx, store.InsertPaymentEventParams{Provider: provider, EventID: ev.ID, Payload: body})
		if err != nil || fresh == 0 {
			return err // duplicate delivery is a no-op
		}
		pay, err := q.LockPaymentByProviderOrderID(ctx, store.LockPaymentByProviderOrderIDParams{Provider: provider, ProviderOrderID: ev.ProviderOrderID})
		if errors.Is(err, pgx.ErrNoRows) {
			s.log.Warn("webhook for unknown gateway order", "provider", provider, "provider_order_id", ev.ProviderOrderID)
			return nil
		}
		if err != nil {
			return err
		}
		if pay.Status == "expired" && ev.Type == payment.EventCaptured {
			// Customer paid after the reservation lapsed: never auto-accept (stock was released).
			s.log.Error("payment captured after order expiry; refund or reinstate manually", "order_id", pay.OrderID, "provider_payment_id", ev.ProviderPaymentID)
			return event(ctx, q, pay.OrderID, "payment_captured_after_expiry", ev)
		}
		if pay.Status != "created" {
			return nil // already final; events can arrive out of order
		}
		switch ev.Type {
		case payment.EventCaptured:
			if ev.AmountPaise != pay.AmountPaise {
				s.log.Error("captured amount mismatch; manual review", "order_id", pay.OrderID, "expected", pay.AmountPaise, "got", ev.AmountPaise)
				return event(ctx, q, pay.OrderID, "payment_amount_mismatch", ev)
			}
			if err := q.UpdatePaymentStatus(ctx, store.UpdatePaymentStatusParams{ID: pay.ID, Status: "captured", ProviderPaymentID: ev.ProviderPaymentID}); err != nil {
				return err
			}
			if err := q.UpdateOrderPaymentState(ctx, store.UpdateOrderPaymentStateParams{ID: pay.OrderID, Status: "placed", PaymentStatus: "paid"}); err != nil {
				return err
			}
			if err := s.clearCart(ctx, q, pay.OrderID); err != nil {
				return err
			}
			return event(ctx, q, pay.OrderID, "payment_captured", ev)
		case payment.EventFailed:
			if err := q.UpdatePaymentStatus(ctx, store.UpdatePaymentStatusParams{ID: pay.ID, Status: "failed", ProviderPaymentID: ev.ProviderPaymentID}); err != nil {
				return err
			}
			if err := q.UpdateOrderPaymentState(ctx, store.UpdateOrderPaymentStateParams{ID: pay.OrderID, Status: "cancelled", PaymentStatus: "failed"}); err != nil {
				return err
			}
			if err := q.ReleaseOrderStock(ctx, pay.OrderID); err != nil {
				return err
			}
			return event(ctx, q, pay.OrderID, "payment_failed", ev)
		default:
			return nil
		}
	})
}

// CompleteMock simulates the gateway for development: it signs an event and runs the real webhook path.
func (s *Service) CompleteMock(ctx context.Context, providerOrderID string, success bool) error {
	m, ok := s.providers["mock"].(payment.Mock)
	if !ok {
		return ErrMockNotEnabled
	}
	pay, err := s.q.LockPaymentByProviderOrderID(ctx, store.LockPaymentByProviderOrderIDParams{Provider: "mock", ProviderOrderID: providerOrderID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	ev := payment.Event{Type: payment.EventFailed, ProviderOrderID: providerOrderID, AmountPaise: pay.AmountPaise}
	if success {
		ev.Type = payment.EventCaptured
	}
	h, body, err := m.SignedEvent(ev)
	if err != nil {
		return err
	}
	return s.HandleWebhook(ctx, "mock", h, body)
}

// clearCart empties the cart of the session that placed the order (payment succeeded).
func (s *Service) clearCart(ctx context.Context, q *store.Queries, orderID uuid.UUID) error {
	o, err := q.GetOrderByID(ctx, orderID)
	if err != nil || o.SessionID == nil {
		return err
	}
	c, err := q.GetCartBySession(ctx, o.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := q.DeleteCartItems(ctx, c.ID); err != nil {
		return err
	}
	if err := q.SetCartNotes(ctx, store.SetCartNotesParams{ID: c.ID, Notes: ""}); err != nil {
		return err
	}
	return q.SetCartAddons(ctx, store.SetCartAddonsParams{ID: c.ID, AddonCodes: []string{}})
}

// ExpirePending cancels unpaid orders older than ttl, releasing their reserved stock. Safe to run
// concurrently from several instances (rows are claimed with SKIP LOCKED). Returns how many expired.
func (s *Service) ExpirePending(ctx context.Context, ttl time.Duration) (int, error) {
	var n int
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		ids, err := q.LockExpiredPendingOrders(ctx, int32(ttl.Minutes()))
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := q.ReleaseOrderStock(ctx, id); err != nil {
				return err
			}
			if err := q.ExpireOrderPayments(ctx, id); err != nil {
				return err
			}
			if err := q.UpdateOrderPaymentState(ctx, store.UpdateOrderPaymentStateParams{ID: id, Status: "cancelled", PaymentStatus: "expired"}); err != nil {
				return err
			}
			if err := event(ctx, q, id, "order_expired", map[string]any{"ttl_minutes": ttl.Minutes()}); err != nil {
				return err
			}
		}
		n = len(ids)
		return nil
	})
	return n, err
}

// RunExpirySweeper calls ExpirePending every interval until ctx is cancelled.
func (s *Service) RunExpirySweeper(ctx context.Context, ttl, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.ExpirePending(ctx, ttl); err != nil {
				s.log.Error("expire pending orders", "err", err)
			} else if n > 0 {
				s.log.Info("expired pending orders", "count", n)
			}
		}
	}
}

func event(ctx context.Context, q *store.Queries, orderID uuid.UUID, typ string, data any) error {
	b, _ := json.Marshal(data)
	return q.InsertOrderEvent(ctx, store.InsertOrderEventParams{OrderID: orderID, Type: typ, Data: b})
}

// newRef returns an order reference like PV-7KQ2M9XA (no ambiguous characters).
func newRef() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "PV-" + string(b)
}
