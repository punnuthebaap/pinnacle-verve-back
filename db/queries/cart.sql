-- name: UpsertSession :exec
INSERT INTO sessions (id, expires_at) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET last_seen = now(), expires_at = EXCLUDED.expires_at;

-- name: GetCartBySession :one
SELECT id, addon_codes, notes FROM carts WHERE session_id = $1;

-- name: CreateCart :one
INSERT INTO carts (session_id) VALUES ($1)
ON CONFLICT (session_id) DO UPDATE SET updated_at = now()
RETURNING id, addon_codes, notes;

-- name: ListCartLines :many
SELECT ci.id, ci.quantity, v.id AS variant_id, v.sku, v.options, v.price_paise, v.stock,
       v.is_active AND p.is_active AS available,
       p.slug AS product_slug, p.name AS product_name, p.series, p.options AS product_options,
       p.gst_rate_bps,
       coalesce(img.url, '')::text AS image_url, coalesce(img.alt, '')::text AS image_alt
FROM cart_items ci
JOIN product_variants v ON v.id = ci.variant_id
JOIN products p ON p.id = v.product_id
LEFT JOIN LATERAL (
    SELECT i.url, i.alt FROM product_images i WHERE i.product_id = p.id ORDER BY i.position LIMIT 1
) img ON true
WHERE ci.cart_id = $1
ORDER BY ci.created_at, ci.id;

-- name: GetPurchasableVariant :one
SELECT v.id, v.stock
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE v.id = $1 AND v.is_active AND p.is_active;

-- name: GetCartItemQuantity :one
SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

-- name: UpsertCartItem :exec
INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, $3)
ON CONFLICT (cart_id, variant_id) DO UPDATE SET quantity = EXCLUDED.quantity;

-- name: GetCartItemStock :one
SELECT v.stock FROM cart_items ci JOIN product_variants v ON v.id = ci.variant_id
WHERE ci.id = $1 AND ci.cart_id = $2;

-- name: UpdateCartItemQuantity :execrows
UPDATE cart_items SET quantity = $3 WHERE id = $1 AND cart_id = $2;

-- name: DeleteCartItem :execrows
DELETE FROM cart_items WHERE id = $1 AND cart_id = $2;

-- name: SetCartAddons :exec
UPDATE carts SET addon_codes = $2, updated_at = now() WHERE id = $1;

-- name: DeleteCartItems :exec
DELETE FROM cart_items WHERE cart_id = $1;

-- name: ListActiveAddons :many
SELECT code, name, description, price_paise FROM addons WHERE is_active ORDER BY position, code;

-- name: ListActiveAddonsByCodes :many
SELECT code, name, description, price_paise FROM addons
WHERE is_active AND code = ANY(sqlc.arg(codes)::text[])
ORDER BY position, code;

-- name: SetCartNotes :exec
UPDATE carts SET notes = $2, updated_at = now() WHERE id = $1;
