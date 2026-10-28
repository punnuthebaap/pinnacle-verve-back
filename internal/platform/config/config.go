// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env            string // development | production
	Port           string
	DatabaseURL    string
	AutoMigrate    bool
	AllowedOrigins []string
	CookieSecure   bool

	PaymentProvider   string // mock | razorpay
	MockWebhookSecret string

	// Initial commercial values (ROADMAP B4). They seed the store_settings row on first use; after that
	// owners change them in the admin (package settings) and these are only used after a dev reseed.
	WhiteGloveThresholdPaise int64
	ShippingPaise            int64
	AddonGSTRateBps          int64
	EnabledPaymentMethods    []string
	WhatsAppNumber           string // E.164; confirmed by the owner 2026-10-28 (B11)

	// Initial hold time for unpaid orders (also seeds store_settings).
	PendingOrderTTL time.Duration
	SweepInterval   time.Duration

	// Storefront cache revalidation after admin catalogue changes (decision D36).
	StorefrontRevalidateURL string
	RevalidateSecret        string

	// Uploaded media (local disk until hosting is chosen, decision D35).
	MediaDir string

	// Per-IP throttling of abuse-prone endpoints; X-Forwarded-For is trusted only from these proxies.
	RateLimitEnabled bool
	TrustedProxies   []string
}

func (c Config) IsProduction() bool { return c.Env == "production" }

func Load() (Config, error) {
	c := Config{
		Env:                      get("APP_ENV", "development"),
		Port:                     get("PORT", "8080"),
		DatabaseURL:              get("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/pinnacle?sslmode=disable"),
		AutoMigrate:              getBool("AUTO_MIGRATE", true),
		AllowedOrigins:           getList("ALLOWED_ORIGINS", "http://localhost:3000"),
		CookieSecure:             getBool("COOKIE_SECURE", false),
		PaymentProvider:          get("PAYMENT_PROVIDER", "mock"),
		MockWebhookSecret:        get("MOCK_WEBHOOK_SECRET", "dev-mock-secret"),
		WhiteGloveThresholdPaise: getInt("WHITE_GLOVE_THRESHOLD_PAISE", 1_500_000),
		ShippingPaise:            getInt("SHIPPING_PAISE", 0),
		AddonGSTRateBps:          getInt("ADDON_GST_RATE_BPS", 1800),
		EnabledPaymentMethods:    getList("ENABLED_PAYMENT_METHODS", "upi,card,netbanking,emi"),
		WhatsAppNumber:           get("WHATSAPP_NUMBER", "+918084047949"),
		PendingOrderTTL:          time.Duration(getInt("PENDING_ORDER_TTL_MINUTES", 30)) * time.Minute,
		SweepInterval:            time.Minute,
		StorefrontRevalidateURL:  get("STOREFRONT_REVALIDATE_URL", "http://localhost:3000/internal/revalidate"),
		RevalidateSecret:         get("REVALIDATE_SECRET", "dev-revalidate-secret"),
		MediaDir:                 get("MEDIA_DIR", ".local/media"),
		RateLimitEnabled:         getBool("RATE_LIMIT_ENABLED", true),
		TrustedProxies:           getList("TRUSTED_PROXIES", "127.0.0.1/32,::1/128"),
	}
	if c.IsProduction() {
		if c.PaymentProvider == "mock" {
			return c, fmt.Errorf("PAYMENT_PROVIDER=mock is not allowed in production")
		}
		if !c.CookieSecure {
			return c, fmt.Errorf("COOKIE_SECURE must be true in production")
		}
		if c.RevalidateSecret == "dev-revalidate-secret" || len(c.RevalidateSecret) < 24 {
			return c, fmt.Errorf("REVALIDATE_SECRET must be set to a strong value in production")
		}
	}
	return c, nil
}

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	v, err := strconv.ParseBool(get(key, strconv.FormatBool(def)))
	if err != nil {
		return def
	}
	return v
}

func getInt(key string, def int64) int64 {
	v, err := strconv.ParseInt(get(key, strconv.FormatInt(def, 10)), 10, 64)
	if err != nil {
		return def
	}
	return v
}

func getList(key, def string) []string {
	var out []string
	for _, s := range strings.Split(get(key, def), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
