-- name: GetContentDocument :one
SELECT d.data, d.updated_at, coalesce(u.name, '')::text AS updated_by_name
FROM content_documents d
LEFT JOIN admin_users u ON u.id = d.updated_by
WHERE d.key = $1;

-- name: UpsertContentDocument :exec
INSERT INTO content_documents (key, data, updated_by, updated_at) VALUES ($1, $2, $3, now())
ON CONFLICT (key) DO UPDATE SET data = EXCLUDED.data, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: ListContentPages :many
SELECT p.slug, p.title, p.published, p.updated_at, coalesce(u.name, '')::text AS updated_by_name
FROM content_pages p
LEFT JOIN admin_users u ON u.id = p.updated_by
ORDER BY p.slug;

-- name: GetContentPage :one
SELECT p.slug, p.title, p.body, p.published, p.updated_at, coalesce(u.name, '')::text AS updated_by_name
FROM content_pages p
LEFT JOIN admin_users u ON u.id = p.updated_by
WHERE p.slug = $1;

-- name: GetPublishedContentPage :one
SELECT slug, title, body, updated_at FROM content_pages WHERE slug = $1 AND published;

-- name: UpsertContentPage :exec
INSERT INTO content_pages (slug, title, body, published, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (slug) DO UPDATE
SET title = EXCLUDED.title, body = EXCLUDED.body, published = EXCLUDED.published, updated_by = EXCLUDED.updated_by, updated_at = now();
