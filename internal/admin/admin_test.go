package admin

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format: %s", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery staple"); err != nil || !ok {
		t.Fatalf("valid password rejected: %v", err)
	}
	if ok, _ := VerifyPassword(h, "Correct horse battery staple"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if _, err := VerifyPassword("$bcrypt$x", "x"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestCan(t *testing.T) {
	cases := []struct {
		role string
		area Area
		want bool
	}{
		{"owner", AreaUsers, true},
		{"owner", AreaCatalog, true},
		{"catalog", AreaCatalog, true},
		{"catalog", AreaOrders, false},
		{"orders", AreaUsers, false},
		{"content", AreaSettings, false},
	}
	for _, c := range cases {
		if got := Can(c.role, c.area); got != c.want {
			t.Errorf("Can(%s, %s) = %v, want %v", c.role, c.area, got, c.want)
		}
	}
}
