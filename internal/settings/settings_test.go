package settings

import (
	"errors"
	"testing"
	"time"
)

func TestWhatsAppNumberValidation(t *testing.T) {
	base := Commercial{PaymentMethods: []string{"upi"}, PendingOrderTTL: 30 * time.Minute}
	for in, want := range map[string]string{
		"+91-8084047949":     "+918084047949",
		" +91 80840 47949 ":  "+918084047949",
		"+91 (808) 404.7949": "+918084047949",
		"":                   "",
	} {
		c := base
		c.WhatsAppNumber = in
		got, err := validate(c)
		if err != nil || got.WhatsAppNumber != want {
			t.Errorf("validate(%q) = %q, %v; want %q", in, got.WhatsAppNumber, err, want)
		}
	}
	for _, bad := range []string{"8084047949", "+0123456789", "+91 80840 4794x", "+1234567"} {
		c := base
		c.WhatsAppNumber = bad
		var inv *InvalidError
		if _, err := validate(c); !errors.As(err, &inv) {
			t.Errorf("validate(%q) err = %v, want InvalidError", bad, err)
		}
	}
}
