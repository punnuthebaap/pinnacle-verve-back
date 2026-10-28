-- Admin catalogue management (phase A2). Unlike storefront queries these include inactive rows.

-- name: AdminListCategories :many
SELECT c.*, (SELECT count(*) FROM products p WHERE p.category_id = c.id)::int AS product_count
FROM categories c
ORDER BY c.position, c.name;

-- name: AdminGetCategory :one
SELECT * FROM categories WHERE id = $1;

-- name: AdminInsertCategory :one
INSERT INTO categories (slug, name, eyebrow, description, position, is_active)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: AdminUpdateCategory :one
UPDATE categories SET slug = $2, name = $3, eyebrow = $4, description = $5, position = $6, is_active = $7
WHERE id = $1
RETURNING *;

-- name: AdminListCategoryFacets :many
SELECT * FROM category_facets WHERE category_id = $1 ORDER BY position, key;

-- name: AdminDeleteCategoryFacets :exec
DELETE FROM category_facets WHERE category_id = $1;

-- name: AdminInsertCategoryFacet :exec
INSERT INTO category_facets (category_id, key, label, kind, position) VALUES ($1, $2, $3, $4, $5);

-- name: AdminListProducts :many
SELECT p.id, p.slug, p.name, p.is_active, p.rank, p.created_at,
       c.id AS category_id, c.name AS category_name,
       coalesce(v.variant_count, 0)::int AS variant_count,
       coalesce(v.total_stock, 0)::int AS total_stock,
       coalesce(v.min_price, 0)::bigint AS min_price_paise,
       coalesce(img.url, '')::text AS image_url,
       count(*) OVER ()::int AS total_count
FROM products p
JOIN categories c ON c.id = p.category_id
LEFT JOIN LATERAL (
    SELECT count(*) AS variant_count, sum(stock) AS total_stock, min(price_paise) AS min_price
    FROM product_variants WHERE product_id = p.id AND is_active
) v ON true
LEFT JOIN LATERAL (
    SELECT i.url FROM product_images i WHERE i.product_id = p.id ORDER BY i.position LIMIT 1
) img ON true
WHERE (sqlc.narg(category_id)::uuid IS NULL OR p.category_id = sqlc.narg(category_id))
  AND (sqlc.narg(active)::bool IS NULL OR p.is_active = sqlc.narg(active))
  AND (sqlc.arg(q)::text = '' OR p.name ILIKE '%' || sqlc.arg(q) || '%' OR p.slug ILIKE '%' || sqlc.arg(q) || '%'
       OR EXISTS (SELECT 1 FROM product_variants sv WHERE sv.product_id = p.id AND sv.sku ILIKE '%' || sqlc.arg(q) || '%'))
ORDER BY c.position, p.rank, p.name
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminGetProduct :one
SELECT * FROM products WHERE id = $1;

-- name: AdminInsertProduct :one
INSERT INTO products (slug, category_id, name, subtitle, series, material_label, size_label, description,
                      shipping_note, badges, features, options, specs, care, facts, gst_rate_bps, rank, is_active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
RETURNING id;

-- name: AdminUpdateProduct :exec
UPDATE products SET slug = $2, category_id = $3, name = $4, subtitle = $5, series = $6, material_label = $7,
    size_label = $8, description = $9, shipping_note = $10, badges = $11, features = $12, options = $13,
    specs = $14, care = $15, facts = $16, gst_rate_bps = $17, rank = $18, is_active = $19
WHERE id = $1;

-- name: AdminListVariants :many
SELECT * FROM product_variants WHERE product_id = $1 ORDER BY position, sku;

-- name: AdminUpsertVariant :exec
INSERT INTO product_variants (id, product_id, sku, options, price_paise, mrp_paise, stock, position, is_active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET sku = EXCLUDED.sku, options = EXCLUDED.options, price_paise = EXCLUDED.price_paise,
    mrp_paise = EXCLUDED.mrp_paise, stock = EXCLUDED.stock, position = EXCLUDED.position, is_active = EXCLUDED.is_active
WHERE product_variants.product_id = EXCLUDED.product_id;

-- Variants are never deleted (orders reference them); ones left out of an update are deactivated.
-- name: AdminDeactivateVariantsNotIn :exec
UPDATE product_variants SET is_active = false WHERE product_id = $1 AND NOT (id = ANY(sqlc.arg(keep)::uuid[]));

-- name: AdminListAttributes :many
SELECT key, value, label, swatch_hex FROM product_attributes WHERE product_id = $1 ORDER BY key, label;

-- name: AdminDeleteAttributes :exec
DELETE FROM product_attributes WHERE product_id = $1;

-- name: AdminInsertAttribute :exec
INSERT INTO product_attributes (product_id, key, value, label, swatch_hex) VALUES ($1, $2, $3, $4, $5);

-- name: AdminListRelations :many
SELECT r.related_id, p.name, p.slug FROM product_relations r JOIN products p ON p.id = r.related_id
WHERE r.product_id = $1 ORDER BY r.position;

-- name: AdminDeleteRelations :exec
DELETE FROM product_relations WHERE product_id = $1;

-- name: AdminInsertRelation :exec
INSERT INTO product_relations (product_id, related_id, position) VALUES ($1, $2, $3);

-- name: AdminListProductImages :many
SELECT id, url, alt, position FROM product_images WHERE product_id = $1 ORDER BY position, id;

-- name: AdminCountProductImages :one
SELECT count(*)::int FROM product_images WHERE product_id = $1;

-- name: AdminInsertProductImage :one
INSERT INTO product_images (product_id, url, alt, storage_key, position)
VALUES ($1, $2, $3, $4, (SELECT coalesce(max(position), -1) + 1 FROM product_images WHERE product_id = $1))
RETURNING id;

-- name: AdminUpdateProductImage :execrows
UPDATE product_images SET alt = $3, position = $4 WHERE id = $1 AND product_id = $2;

-- name: AdminDeleteProductImage :one
DELETE FROM product_images WHERE id = $1 AND product_id = $2 RETURNING storage_key;
