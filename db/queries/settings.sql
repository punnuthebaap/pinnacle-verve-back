-- name: GetStoreSettings :one
SELECT s.white_glove_threshold_paise, s.shipping_paise, s.addon_gst_rate_bps, s.payment_methods,
       s.pending_order_ttl_minutes, s.whatsapp_number, s.updated_at, coalesce(u.name, '')::text AS updated_by_name
FROM store_settings s
LEFT JOIN admin_users u ON u.id = s.updated_by;

-- name: InsertDefaultStoreSettings :exec
INSERT INTO store_settings (white_glove_threshold_paise, shipping_paise, addon_gst_rate_bps, payment_methods, pending_order_ttl_minutes, whatsapp_number)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING;

-- name: LockStoreSettings :one
SELECT * FROM store_settings FOR UPDATE;

-- name: UpdateStoreSettings :exec
UPDATE store_settings
SET white_glove_threshold_paise = $1, shipping_paise = $2, addon_gst_rate_bps = $3, payment_methods = $4,
    pending_order_ttl_minutes = $5, whatsapp_number = $6, updated_by = $7, updated_at = now();

-- name: AdminListAddons :many
SELECT * FROM addons ORDER BY position, code;

-- name: AdminGetAddon :one
SELECT * FROM addons WHERE code = $1;

-- name: AdminUpsertAddon :exec
INSERT INTO addons (code, name, description, price_paise, is_active, position)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name, description = EXCLUDED.description, price_paise = EXCLUDED.price_paise,
    is_active = EXCLUDED.is_active, position = EXCLUDED.position;
