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

	// Commercial values not yet confirmed by the business (see ROADMAP B4); keep configurable.
	WhiteGloveThresholdPaise int64
	ShippingPaise            int64
	AddonGSTRateBps          int64
	EnabledPaymentMethods    []string

	// Unpaid orders are cancelled and their stock released after this long.
	PendingOrderTTL time.Duration
	SweepInterval   time.Duration
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
		PendingOrderTTL:          time.Duration(getInt("PENDING_ORDER_TTL_MINUTES", 30)) * time.Minute,
		SweepInterval:            time.Minute,
	}
	if c.IsProduction() {
		if c.PaymentProvider == "mock" {
			return c, fmt.Errorf("PAYMENT_PROVIDER=mock is not allowed in production")
		}
		if !c.CookieSecure {
			return c, fmt.Errorf("COOKIE_SECURE must be true in production")
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
