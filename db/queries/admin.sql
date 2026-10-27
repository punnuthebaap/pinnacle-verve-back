-- name: GetAdminByEmail :one
SELECT * FROM admin_users WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: GetAdminByID :one
SELECT * FROM admin_users WHERE id = $1;

-- name: ListAdmins :many
SELECT * FROM admin_users ORDER BY created_at;

-- name: CountActiveOwners :one
SELECT count(*)::int FROM admin_users WHERE role = 'owner' AND is_active;

-- name: InsertAdmin :one
INSERT INTO admin_users (email, name, password_hash, role)
VALUES (lower(sqlc.arg(email)::text), sqlc.arg(name), sqlc.arg(password_hash), sqlc.arg(role))
RETURNING *;

-- name: UpdateAdmin :one
UPDATE admin_users SET name = $2, role = $3, is_active = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAdminPassword :exec
UPDATE admin_users SET password_hash = $2, failed_attempts = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- Locks the account for lock_minutes once failed attempts reach max_attempts.
-- name: RecordAdminLoginFailure :one
UPDATE admin_users SET
  failed_attempts = failed_attempts + 1,
  locked_until = CASE WHEN failed_attempts + 1 >= sqlc.arg(max_attempts)::int
                      THEN now() + make_interval(mins => sqlc.arg(lock_minutes)::int)
                      ELSE locked_until END
WHERE id = sqlc.arg(id)
RETURNING failed_attempts, locked_until;

-- name: RecordAdminLoginSuccess :exec
UPDATE admin_users SET failed_attempts = 0, locked_until = NULL, last_login_at = now() WHERE id = $1;

-- name: InsertAdminSession :exec
INSERT INTO admin_sessions (id, admin_id, ip, user_agent, expires_at) VALUES ($1, $2, $3, $4, $5);

-- Valid = not past absolute expiry, not idle too long, and the account is active.
-- name: GetActiveAdminSession :one
SELECT a.*
FROM admin_sessions s
JOIN admin_users a ON a.id = s.admin_id
WHERE s.id = sqlc.arg(id) AND s.expires_at > now() AND a.is_active
  AND s.last_seen > now() - make_interval(mins => sqlc.arg(idle_minutes)::int);

-- name: TouchAdminSession :exec
UPDATE admin_sessions SET last_seen = now() WHERE id = $1;

-- name: DeleteAdminSession :exec
DELETE FROM admin_sessions WHERE id = $1;

-- name: DeleteAdminSessionsExcept :exec
DELETE FROM admin_sessions WHERE admin_id = $1 AND id <> sqlc.arg(keep)::bytea;

-- name: DeleteAdminSessions :exec
DELETE FROM admin_sessions WHERE admin_id = $1;

-- name: InsertAdminAudit :exec
INSERT INTO admin_audit (admin_id, action, entity, entity_id, data, ip)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAdminAudit :many
SELECT l.id, l.action, l.entity, l.entity_id, l.data, l.ip, l.created_at,
       coalesce(a.email, '')::text AS admin_email,
       count(*) OVER ()::int AS total_count
FROM admin_audit l
LEFT JOIN admin_users a ON a.id = l.admin_id
ORDER BY l.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
