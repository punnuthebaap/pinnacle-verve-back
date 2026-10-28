-- +goose Up
-- Editable storefront copy (A6). The storefront ships the original copy and uses it until a document is
-- saved here. Content pages hold owner-supplied text (e.g. legal pages, ROADMAP B7).

CREATE TABLE content_documents (
    key        text PRIMARY KEY CHECK (key IN ('site', 'home')),
    data       jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES admin_users(id) ON DELETE SET NULL
);

CREATE TABLE content_pages (
    slug       text PRIMARY KEY CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 80),
    title      text NOT NULL CHECK (length(title) BETWEEN 1 AND 160),
    body       text NOT NULL CHECK (length(body) <= 20000),
    published  boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES admin_users(id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE content_pages, content_documents;
