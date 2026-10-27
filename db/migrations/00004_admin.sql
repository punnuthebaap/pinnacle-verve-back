-- +goose Up
-- Admin panel (decisions D32–D35): staff accounts with roles, server-side sessions and an audit log.

CREATE TABLE admin_users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           text NOT NULL,
    name            text NOT NULL,
    password_hash   text NOT NULL,                -- argon2id, PHC string format
    role            text NOT NULL CHECK (role IN ('owner', 'catalog', 'orders', 'content')),
    is_active       boolean NOT NULL DEFAULT true,
    failed_attempts int NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    last_login_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX admin_users_email_idx ON admin_users (lower(email));

-- id is SHA-256 of the cookie token (the raw token is never stored).
CREATE TABLE admin_sessions (
    id         bytea PRIMARY KEY,
    admin_id   uuid NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX admin_sessions_admin_idx ON admin_sessions (admin_id);

CREATE TABLE admin_audit (
    id         bigserial PRIMARY KEY,
    admin_id   uuid REFERENCES admin_users(id) ON DELETE SET NULL,
    action     text NOT NULL,              -- e.g. auth.login, users.create, product.update
    entity     text NOT NULL DEFAULT '',
    entity_id  text NOT NULL DEFAULT '',
    data       jsonb NOT NULL DEFAULT '{}',
    ip         text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_created_idx ON admin_audit (created_at DESC);

-- +goose Down
DROP TABLE admin_audit, admin_sessions, admin_users;
