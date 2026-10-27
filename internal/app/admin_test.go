package app

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/store"
)

// Admin authentication, lockout, roles, last-owner protection, session revocation and audit.
func TestAdminAuth(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "TRUNCATE admin_users CASCADE"); err != nil {
		t.Fatal(err)
	}
	hash, _ := admin.HashPassword("owner-password-123")
	if _, err := store.New(s.pool).InsertAdmin(ctx, store.InsertAdminParams{Email: "Owner@Example.com", Name: "Owner", PasswordHash: hash, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	newClient := func() *client {
		jar, _ := cookiejar.New(nil)
		return &client{t: t, base: s.c.base, origin: "http://localhost:3000", http: &http.Client{Jar: jar}}
	}
	login := func(c *client, email, pw string, want int) {
		t.Helper()
		c.do("POST", "/admin/session", "", map[string]any{"email": email, "password": pw}, want, nil)
	}

	anon := newClient()
	anon.do("GET", "/admin/me", "", nil, 401, nil)
	anon.do("GET", "/admin/users", "", nil, 401, nil)
	login(anon, "nobody@example.com", "whatever-password", 401) // unknown email: same response as wrong password

	// Lockout after 5 failures; correct password is refused while locked.
	for i := 0; i < 4; i++ {
		login(anon, "owner@example.com", "wrong-password-xx", 401)
	}
	login(anon, "owner@example.com", "wrong-password-xx", 429)
	login(anon, "owner@example.com", "owner-password-123", 429)
	if _, err := s.pool.Exec(ctx, "UPDATE admin_users SET locked_until = NULL, failed_attempts = 0"); err != nil {
		t.Fatal(err)
	}

	owner := newClient()
	login(owner, "OWNER@example.com", "owner-password-123", 200) // email is case-insensitive
	var me struct{ Email, Role string }
	owner.do("GET", "/admin/me", "", nil, 200, &me)
	if me.Email != "owner@example.com" || me.Role != "owner" {
		t.Fatalf("me = %+v", me)
	}

	var created struct{ ID string }
	owner.do("POST", "/admin/users", "", map[string]any{"email": "cat@example.com", "name": "Cat Manager", "role": "catalog", "password": "short"}, 400, nil)
	owner.do("POST", "/admin/users", "", map[string]any{"email": "cat@example.com", "name": "Cat Manager", "role": "catalog", "password": "catalog-pass-123"}, 201, &created)
	owner.do("POST", "/admin/users", "", map[string]any{"email": "CAT@example.com", "name": "Dup", "role": "orders", "password": "catalog-pass-123"}, 409, nil)

	cat := newClient()
	login(cat, "cat@example.com", "catalog-pass-123", 200)
	cat.do("GET", "/admin/users", "", nil, 403, nil) // catalog role can't manage users
	cat.do("GET", "/admin/audit", "", nil, 403, nil)

	// The only owner can't demote themselves.
	var users struct{ Items []struct{ ID, Role string } }
	owner.do("GET", "/admin/users", "", nil, 200, &users)
	var ownerID string
	for _, u := range users.Items {
		if u.Role == "owner" {
			ownerID = u.ID
		}
	}
	owner.do("PATCH", "/admin/users/"+ownerID, "", map[string]any{"role": "catalog"}, 409, nil)

	// Disabling an account signs it out immediately.
	owner.do("PATCH", "/admin/users/"+created.ID, "", map[string]any{"isActive": false}, 200, nil)
	cat.do("GET", "/admin/me", "", nil, 401, nil)
	login(cat, "cat@example.com", "catalog-pass-123", 401)

	// Changing your password keeps this session and revokes the others.
	other := newClient()
	login(other, "owner@example.com", "owner-password-123", 200)
	owner.do("PUT", "/admin/me/password", "", map[string]any{"currentPassword": "nope-nope-nope", "newPassword": "owner-password-456"}, 422, nil)
	owner.do("PUT", "/admin/me/password", "", map[string]any{"currentPassword": "owner-password-123", "newPassword": "owner-password-456"}, 204, nil)
	owner.do("GET", "/admin/me", "", nil, 200, nil)
	other.do("GET", "/admin/me", "", nil, 401, nil)

	var audit struct {
		Items []struct{ Action string }
		Total int
	}
	owner.do("GET", "/admin/audit", "", nil, 200, &audit)
	actions := map[string]bool{}
	for _, e := range audit.Items {
		actions[e.Action] = true
	}
	for _, want := range []string{"auth.login", "auth.login_failed", "auth.login_locked", "users.create", "users.update", "auth.password_changed"} {
		if !actions[want] {
			t.Errorf("audit missing %q (have %v)", want, actions)
		}
	}

	owner.do("DELETE", "/admin/session", "", nil, 204, nil)
	owner.do("GET", "/admin/me", "", nil, 401, nil)
}
