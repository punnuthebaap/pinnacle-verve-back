package config

import (
	"strings"
	"testing"
)

func TestLoadDeployedRules(t *testing.T) {
	strong := strings.Repeat("s", 32)
	deployed := map[string]string{
		"COOKIE_SECURE":       "true",
		"REVALIDATE_SECRET":   strong,
		"MOCK_WEBHOOK_SECRET": strong,
	}
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string // substring; empty = loads
	}{
		{"development defaults", map[string]string{}, ""},
		{"unknown env", map[string]string{"APP_ENV": "prod"}, "APP_ENV must be"},
		{"staging with mock payments", with(deployed, "APP_ENV", "staging"), ""},
		{"staging needs secure cookies", with(with(deployed, "APP_ENV", "staging"), "COOKIE_SECURE", "false"), "COOKIE_SECURE"},
		{"staging needs a revalidate secret", with(with(deployed, "APP_ENV", "staging"), "REVALIDATE_SECRET", "dev-revalidate-secret"), "REVALIDATE_SECRET"},
		{"staging needs a mock webhook secret", with(with(deployed, "APP_ENV", "staging"), "MOCK_WEBHOOK_SECRET", "short"), "MOCK_WEBHOOK_SECRET"},
		{"production refuses mock payments", with(deployed, "APP_ENV", "production"), "PAYMENT_PROVIDER=mock"},
	}
	keys := []string{"APP_ENV", "COOKIE_SECURE", "REVALIDATE_SECRET", "MOCK_WEBHOOK_SECRET", "PAYMENT_PROVIDER"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range keys {
				t.Setenv(k, tc.env[k]) // "" = unset for get()
			}
			_, err := Load()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func with(m map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for key, val := range m {
		if key != k {
			out[key] = val
		}
	}
	return out
}
