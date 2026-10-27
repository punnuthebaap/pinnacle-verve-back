// Package admin implements staff accounts for the admin panel: sign-in with lockout, server-side
// sessions, role-based access and an audit log of every change.
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pinnacleverve/api/internal/platform/httpx"
	"github.com/pinnacleverve/api/internal/store"
)

const (
	CookieName   = "pv_admin"
	cookiePath   = "/api/v1/admin"
	SessionTTL   = 12 * time.Hour
	IdleMinutes  = 120
	MaxAttempts  = 5
	LockMinutes  = 15
	AuditPerPage = 50
)

var (
	ErrInvalidCredentials = errors.New("admin: invalid email or password")
	ErrLocked             = errors.New("admin: account temporarily locked")
	ErrForbidden          = errors.New("admin: not allowed for this role")
	ErrNotFound           = errors.New("admin: not found")
	ErrEmailTaken         = errors.New("admin: email already in use")
	ErrLastOwner          = errors.New("admin: the last active owner cannot be demoted or disabled")
)

// Area is a permission scope. Owners can do everything; other roles only their own area.
type Area string

const (
	AreaCatalog  Area = "catalog"
	AreaOrders   Area = "orders"
	AreaContent  Area = "content"
	AreaUsers    Area = "users"
	AreaSettings Area = "settings"
	AreaAudit    Area = "audit"
)

func Can(role string, area Area) bool {
	return role == "owner" || role == string(area)
}

type Service struct {
	q   *store.Queries
	log *slog.Logger
	// dummyHash is verified against when an email is unknown, so response time doesn't reveal accounts.
	dummyHash string
}

func NewService(q *store.Queries, log *slog.Logger) *Service {
	h, _ := HashPassword("not-a-real-password-just-for-timing")
	return &Service{q: q, log: log, dummyHash: h}
}

// Login verifies credentials, applies lockout, and on success opens a session and sets its cookie.
func (s *Service) Login(ctx context.Context, email, password string) (store.AdminUser, error) {
	u, err := s.q.GetAdminByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !u.IsActive) {
		_, _ = VerifyPassword(s.dummyHash, password)
		s.Audit(ctx, nil, "auth.login_failed", "admin", "", map[string]any{"email": strings.ToLower(strings.TrimSpace(email))})
		return store.AdminUser{}, ErrInvalidCredentials
	}
	if err != nil {
		return store.AdminUser{}, err
	}
	if u.LockedUntil.Valid && u.LockedUntil.Time.After(time.Now()) {
		s.Audit(ctx, &u.ID, "auth.login_locked", "admin", u.ID.String(), nil)
		return store.AdminUser{}, ErrLocked
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return store.AdminUser{}, err
	}
	if !ok {
		res, err := s.q.RecordAdminLoginFailure(ctx, store.RecordAdminLoginFailureParams{ID: u.ID, MaxAttempts: MaxAttempts, LockMinutes: LockMinutes})
		if err != nil {
			return store.AdminUser{}, err
		}
		s.Audit(ctx, &u.ID, "auth.login_failed", "admin", u.ID.String(), map[string]any{"attempts": res.FailedAttempts})
		if res.LockedUntil.Valid && res.LockedUntil.Time.After(time.Now()) {
			return store.AdminUser{}, ErrLocked
		}
		return store.AdminUser{}, ErrInvalidCredentials
	}
	if err := s.q.RecordAdminLoginSuccess(ctx, u.ID); err != nil {
		return store.AdminUser{}, err
	}
	if err := s.openSession(ctx, u.ID); err != nil {
		return store.AdminUser{}, err
	}
	s.Audit(ctx, &u.ID, "auth.login", "admin", u.ID.String(), nil)
	return u, nil
}

func (s *Service) Logout(ctx context.Context) error {
	st := state(ctx)
	if st.sessionID != nil {
		if err := s.q.DeleteAdminSession(ctx, st.sessionID); err != nil {
			return err
		}
	}
	if st.admin != nil {
		s.Audit(ctx, &st.admin.ID, "auth.logout", "admin", st.admin.ID.String(), nil)
	}
	http.SetCookie(st.w, &http.Cookie{Name: CookieName, Value: "", Path: cookiePath, MaxAge: -1, HttpOnly: true, Secure: st.secure, SameSite: http.SameSiteStrictMode})
	return nil
}

func (s *Service) ChangeOwnPassword(ctx context.Context, current, next string) error {
	me := Current(ctx)
	ok, err := VerifyPassword(me.PasswordHash, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.q.SetAdminPassword(ctx, store.SetAdminPasswordParams{ID: me.ID, PasswordHash: hash}); err != nil {
		return err
	}
	if err := s.q.DeleteAdminSessionsExcept(ctx, store.DeleteAdminSessionsExceptParams{AdminID: me.ID, Keep: state(ctx).sessionID}); err != nil {
		return err
	}
	s.Audit(ctx, &me.ID, "auth.password_changed", "admin", me.ID.String(), nil)
	return nil
}

func (s *Service) Users(ctx context.Context) ([]store.AdminUser, error) {
	if !Can(Current(ctx).Role, AreaUsers) {
		return nil, ErrForbidden
	}
	return s.q.ListAdmins(ctx)
}

type NewUser struct{ Email, Name, Role, Password string }

func (s *Service) CreateUser(ctx context.Context, in NewUser) (store.AdminUser, error) {
	me := Current(ctx)
	if !Can(me.Role, AreaUsers) {
		return store.AdminUser{}, ErrForbidden
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return store.AdminUser{}, err
	}
	u, err := s.q.InsertAdmin(ctx, store.InsertAdminParams{Email: strings.TrimSpace(in.Email), Name: strings.TrimSpace(in.Name), PasswordHash: hash, Role: in.Role})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return store.AdminUser{}, ErrEmailTaken
	}
	if err != nil {
		return store.AdminUser{}, err
	}
	s.Audit(ctx, &me.ID, "users.create", "admin", u.ID.String(), map[string]any{"email": u.Email, "role": u.Role})
	return u, nil
}

type UserPatch struct {
	Name     *string
	Role     *string
	IsActive *bool
	Password *string
}

func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, p UserPatch) (store.AdminUser, error) {
	me := Current(ctx)
	if !Can(me.Role, AreaUsers) {
		return store.AdminUser{}, ErrForbidden
	}
	u, err := s.q.GetAdminByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.AdminUser{}, ErrNotFound
	}
	if err != nil {
		return store.AdminUser{}, err
	}
	name, role, active := u.Name, u.Role, u.IsActive
	if p.Name != nil {
		name = strings.TrimSpace(*p.Name)
	}
	if p.Role != nil {
		role = *p.Role
	}
	if p.IsActive != nil {
		active = *p.IsActive
	}
	if u.Role == "owner" && u.IsActive && (role != "owner" || !active) {
		n, err := s.q.CountActiveOwners(ctx)
		if err != nil {
			return store.AdminUser{}, err
		}
		if n <= 1 {
			return store.AdminUser{}, ErrLastOwner
		}
	}
	u, err = s.q.UpdateAdmin(ctx, store.UpdateAdminParams{ID: id, Name: name, Role: role, IsActive: active})
	if err != nil {
		return store.AdminUser{}, err
	}
	changes := map[string]any{"name": name, "role": role, "isActive": active}
	if p.Password != nil {
		hash, err := HashPassword(*p.Password)
		if err != nil {
			return store.AdminUser{}, err
		}
		if err := s.q.SetAdminPassword(ctx, store.SetAdminPasswordParams{ID: id, PasswordHash: hash}); err != nil {
			return store.AdminUser{}, err
		}
		changes["passwordReset"] = true
	}
	if p.Password != nil || !active {
		if err := s.q.DeleteAdminSessions(ctx, id); err != nil {
			return store.AdminUser{}, err
		}
	}
	s.Audit(ctx, &me.ID, "users.update", "admin", id.String(), changes)
	return u, nil
}

func (s *Service) AuditLog(ctx context.Context, page int) ([]store.ListAdminAuditRow, int, error) {
	if !Can(Current(ctx).Role, AreaAudit) {
		return nil, 0, ErrForbidden
	}
	rows, err := s.q.ListAdminAudit(ctx, store.ListAdminAuditParams{Lim: AuditPerPage, Off: int32((page - 1) * AuditPerPage)})
	if err != nil || len(rows) == 0 {
		return rows, 0, err
	}
	return rows, int(rows[0].TotalCount), nil
}

// Audit records an admin action. Failures are logged, never surfaced: auditing must not block work.
// q may be transaction-bound by callers that need the entry to commit atomically with the change.
func (s *Service) Audit(ctx context.Context, adminID *uuid.UUID, action, entity, entityID string, data any) {
	s.AuditWith(ctx, s.q, adminID, action, entity, entityID, data)
}

func (s *Service) AuditWith(ctx context.Context, q *store.Queries, adminID *uuid.UUID, action, entity, entityID string, data any) {
	b, _ := json.Marshal(data)
	if data == nil {
		b = []byte("{}")
	}
	var id pgtype.UUID
	if adminID != nil {
		id = pgtype.UUID{Bytes: *adminID, Valid: true}
	}
	if err := q.InsertAdminAudit(ctx, store.InsertAdminAuditParams{AdminID: id, Action: action, Entity: entity, EntityID: entityID, Data: b, Ip: state(ctx).ip}); err != nil {
		s.log.Error("admin audit write failed", "action", action, "err", err)
	}
}

func (s *Service) openSession(ctx context.Context, adminID uuid.UUID) error {
	st := state(ctx)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id := hashToken(raw)
	exp := time.Now().Add(SessionTTL)
	if err := s.q.InsertAdminSession(ctx, store.InsertAdminSessionParams{
		ID: id, AdminID: adminID, Ip: st.ip, UserAgent: truncate(st.userAgent, 256), ExpiresAt: pgtype.Timestamptz{Time: exp, Valid: true},
	}); err != nil {
		return err
	}
	http.SetCookie(st.w, &http.Cookie{
		Name: CookieName, Value: base64.RawURLEncoding.EncodeToString(raw), Path: cookiePath,
		MaxAge: int(SessionTTL.Seconds()), HttpOnly: true, Secure: st.secure, SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// ---- request state & middleware ----

type reqState struct {
	w         http.ResponseWriter
	secure    bool
	ip        string
	userAgent string
	sessionID []byte
	admin     *store.AdminUser
}

type ctxKey struct{}

func state(ctx context.Context) *reqState {
	if st, ok := ctx.Value(ctxKey{}).(*reqState); ok {
		return st
	}
	return &reqState{}
}

// Current returns the signed-in admin. Only valid behind Middleware on authenticated routes.
func Current(ctx context.Context) store.AdminUser {
	if st := state(ctx); st.admin != nil {
		return *st.admin
	}
	return store.AdminUser{}
}

// Middleware authenticates every /api/v1/admin request except sign-in; unauthenticated → 401.
func (s *Service) Middleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
				next.ServeHTTP(w, r)
				return
			}
			st := &reqState{w: w, secure: secure, ip: clientIP(r), userAgent: r.UserAgent()}
			ctx := context.WithValue(r.Context(), ctxKey{}, st)
			if c, err := r.Cookie(CookieName); err == nil {
				if raw, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil && len(raw) == 32 {
					id := hashToken(raw)
					if u, err := s.q.GetActiveAdminSession(ctx, store.GetActiveAdminSessionParams{ID: id, IdleMinutes: IdleMinutes}); err == nil {
						st.sessionID, st.admin = id, &u
						_ = s.q.TouchAdminSession(ctx, id)
					}
				}
			}
			isLogin := r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/session"
			if st.admin == nil && !isLogin {
				httpx.WriteProblem(w, httpx.Problem{Title: "Sign in required", Status: http.StatusUnauthorized, Code: "unauthenticated"})
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// clientIP is used for the audit trail only. The Next.js proxy sets X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return truncate(strings.TrimSpace(strings.Split(xff, ",")[0]), 64)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hashToken(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
