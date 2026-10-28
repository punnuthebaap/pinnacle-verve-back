// Package settings holds the store's commercial values (phase A4): the free white-glove threshold,
// shipping charge, add-on GST rate, enabled payment methods and how long unpaid orders are held, plus the
// service add-ons. Owners edit them in the admin; every change is audited with its before/after values.
//
// The values live in one database row, created on first use from the environment defaults. Pricing reads
// them per request (a primary-key lookup), so every instance applies a change immediately.
package settings

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

// PaymentMethods are the methods checkout implements. Cash on delivery needs business rules first
// (advance token, limits; ROADMAP B4), so it can't be enabled here yet.
var PaymentMethods = []string{"upi", "card", "netbanking", "emi"}

// InvalidError is a business-rule violation with a message an admin can act on.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

type Commercial struct {
	WhiteGloveThresholdPaise int64
	ShippingPaise            int64
	AddonGSTRateBps          int64
	PaymentMethods           []string
	PendingOrderTTL          time.Duration
}

type Settings struct {
	Commercial
	UpdatedAt time.Time
	UpdatedBy string // empty until an admin changes something
}

// Defaults are the environment values used to create the settings row.
func Defaults(cfg config.Config) Commercial {
	return Commercial{
		WhiteGloveThresholdPaise: cfg.WhiteGloveThresholdPaise,
		ShippingPaise:            cfg.ShippingPaise,
		AddonGSTRateBps:          cfg.AddonGSTRateBps,
		PaymentMethods:           cfg.EnabledPaymentMethods,
		PendingOrderTTL:          cfg.PendingOrderTTL,
	}
}

type Service struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	audit    *admin.Service
	defaults Commercial
}

func NewService(pool *pgxpool.Pool, q *store.Queries, audit *admin.Service, defaults Commercial) *Service {
	return &Service{pool: pool, q: q, audit: audit, defaults: defaults}
}

// Current returns the values in force, reading with q (which may be transaction-bound; nil = pool).
func (s *Service) Current(ctx context.Context, q *store.Queries) (Commercial, error) {
	st, err := s.read(ctx, q)
	return st.Commercial, err
}

func (s *Service) read(ctx context.Context, q *store.Queries) (Settings, error) {
	if q == nil {
		q = s.q
	}
	row, err := q.GetStoreSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		d := s.defaults
		if err := q.InsertDefaultStoreSettings(ctx, store.InsertDefaultStoreSettingsParams{
			WhiteGloveThresholdPaise: d.WhiteGloveThresholdPaise, ShippingPaise: d.ShippingPaise, AddonGstRateBps: int32(d.AddonGSTRateBps),
			PaymentMethods: d.PaymentMethods, PendingOrderTtlMinutes: int32(d.PendingOrderTTL.Minutes()),
		}); err != nil {
			return Settings{}, err
		}
		row, err = q.GetStoreSettings(ctx)
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Commercial: Commercial{
			WhiteGloveThresholdPaise: row.WhiteGloveThresholdPaise, ShippingPaise: row.ShippingPaise, AddonGSTRateBps: int64(row.AddonGstRateBps),
			PaymentMethods: row.PaymentMethods, PendingOrderTTL: time.Duration(row.PendingOrderTtlMinutes) * time.Minute,
		},
		UpdatedAt: row.UpdatedAt.Time, UpdatedBy: row.UpdatedByName,
	}, nil
}

func authorize(ctx context.Context) (store.AdminUser, error) {
	me := admin.Current(ctx)
	if !admin.Can(me.Role, admin.AreaSettings) {
		return me, admin.ErrForbidden
	}
	return me, nil
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	if _, err := authorize(ctx); err != nil {
		return Settings{}, err
	}
	return s.read(ctx, nil)
}

func validate(c Commercial) (Commercial, error) {
	switch {
	case c.WhiteGloveThresholdPaise < 0 || c.WhiteGloveThresholdPaise > 1_000_000_000:
		return c, invalid("The free white-glove threshold must be between ₹0 and ₹1,00,00,000.")
	case c.ShippingPaise < 0 || c.ShippingPaise > 10_000_000:
		return c, invalid("The shipping charge must be between ₹0 and ₹1,00,000.")
	case c.AddonGSTRateBps < 0 || c.AddonGSTRateBps > 4000:
		return c, invalid("The add-on GST rate must be between 0%% and 40%%.")
	case c.PendingOrderTTL < 10*time.Minute || c.PendingOrderTTL > 24*time.Hour || c.PendingOrderTTL%time.Minute != 0:
		return c, invalid("Unpaid orders must be held for a whole number of minutes between 10 and 1,440.")
	}
	methods := []string{}
	for _, m := range c.PaymentMethods {
		if !slices.Contains(PaymentMethods, m) {
			return c, invalid("%q isn't a payment method checkout supports.", m)
		}
		if !slices.Contains(methods, m) {
			methods = append(methods, m)
		}
	}
	if len(methods) == 0 {
		return c, invalid("Enable at least one payment method.")
	}
	c.PaymentMethods = methods
	return c, nil
}

func (s *Service) Update(ctx context.Context, in Commercial) (Settings, error) {
	me, err := authorize(ctx)
	if err != nil {
		return Settings{}, err
	}
	in, err = validate(in)
	if err != nil {
		return Settings{}, err
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := s.read(ctx, q); err != nil { // ensures the row exists
			return err
		}
		before, err := q.LockStoreSettings(ctx)
		if err != nil {
			return err
		}
		if err := q.UpdateStoreSettings(ctx, store.UpdateStoreSettingsParams{
			WhiteGloveThresholdPaise: in.WhiteGloveThresholdPaise, ShippingPaise: in.ShippingPaise, AddonGstRateBps: int32(in.AddonGSTRateBps),
			PaymentMethods: in.PaymentMethods, PendingOrderTtlMinutes: int32(in.PendingOrderTTL.Minutes()),
			UpdatedBy: pgtype.UUID{Bytes: me.ID, Valid: true},
		}); err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, "settings.update", "settings", "commercial", map[string]any{
			"before": map[string]any{
				"whiteGloveThresholdPaise": before.WhiteGloveThresholdPaise, "shippingPaise": before.ShippingPaise,
				"addonGstRateBps": before.AddonGstRateBps, "paymentMethods": before.PaymentMethods, "pendingOrderTtlMinutes": before.PendingOrderTtlMinutes,
			},
			"after": map[string]any{
				"whiteGloveThresholdPaise": in.WhiteGloveThresholdPaise, "shippingPaise": in.ShippingPaise,
				"addonGstRateBps": in.AddonGSTRateBps, "paymentMethods": in.PaymentMethods, "pendingOrderTtlMinutes": int(in.PendingOrderTTL.Minutes()),
			},
		})
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return s.read(ctx, nil)
}

// ---- add-ons ----

type AddonInput struct {
	Code, Name, Description string
	PricePaise              int64
	IsActive                bool
	Position                int32
}

var addonCode = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (s *Service) Addons(ctx context.Context) ([]store.Addon, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, err
	}
	return s.q.AdminListAddons(ctx)
}

// SaveAddon creates or updates an add-on. Codes are permanent (carts and orders refer to them), so an
// add-on is switched off rather than deleted. Changes apply to open carts immediately.
func (s *Service) SaveAddon(ctx context.Context, in AddonInput) ([]store.Addon, error) {
	me, err := authorize(ctx)
	if err != nil {
		return nil, err
	}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	switch {
	case !addonCode.MatchString(in.Code) || len(in.Code) > 40:
		return nil, invalid("Codes use lowercase letters, numbers and hyphens (max 40), e.g. care-kit.")
	case len(in.Name) < 2:
		return nil, invalid("Give the add-on a name.")
	case in.PricePaise < 0 || in.PricePaise > 10_000_000:
		return nil, invalid("The price must be between ₹0 and ₹1,00,000.")
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		action := "addon.update"
		before, err := q.AdminGetAddon(ctx, in.Code)
		if errors.Is(err, pgx.ErrNoRows) {
			action = "addon.create"
		} else if err != nil {
			return err
		}
		if err := q.AdminUpsertAddon(ctx, store.AdminUpsertAddonParams{
			Code: in.Code, Name: in.Name, Description: in.Description, PricePaise: in.PricePaise, IsActive: in.IsActive, Position: in.Position,
		}); err != nil {
			return err
		}
		data := map[string]any{"name": in.Name, "pricePaise": in.PricePaise, "isActive": in.IsActive}
		if action == "addon.update" {
			data["before"] = map[string]any{"name": before.Name, "pricePaise": before.PricePaise, "isActive": before.IsActive}
		}
		s.audit.AuditWith(ctx, q, &me.ID, action, "addon", in.Code, data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.q.AdminListAddons(ctx)
}
