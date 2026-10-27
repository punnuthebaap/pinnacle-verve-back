-- name: GetOrderIDByIdempotencyKey :one
SELECT id FROM orders WHERE session_id = $1 AND idempotency_key = $2;

-- name: InsertOrder :one
INSERT INTO orders (ref, session_id, idempotency_key, email, shipping_address, gstin, notes,
                    payment_method, subtotal_paise, discount_paise, shipping_paise, addons_paise,
                    tax_paise, total_paise)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING id;

-- name: InsertOrderItem :exec
INSERT INTO order_items (order_id, variant_id, product_slug, product_name, variant_label, sku,
                         image_url, image_alt, unit_price_paise, quantity, line_total_paise)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: InsertOrderAddon :exec
INSERT INTO order_addons (order_id, code, name, description, price_paise)
VALUES ($1, $2, $3, $4, $5);

-- name: InsertOrderEvent :exec
INSERT INTO order_events (order_id, type, data) VALUES ($1, $2, $3);

-- name: ReserveStock :execrows
UPDATE product_variants SET stock = stock - sqlc.arg(qty)::int
WHERE id = sqlc.arg(id) AND stock >= sqlc.arg(qty)::int;

-- name: ReleaseOrderStock :exec
UPDATE product_variants v SET stock = v.stock + oi.quantity
FROM order_items oi
WHERE oi.order_id = $1 AND oi.variant_id = v.id;

-- name: GetOrderByID :one
SELECT * FROM orders WHERE id = $1;

-- name: GetOrderByRefForSession :one
SELECT * FROM orders WHERE ref = $1 AND session_id = $2;

-- name: ListOrderItems :many
SELECT * FROM order_items WHERE order_id = $1 ORDER BY id;

-- name: ListOrderAddons :many
SELECT * FROM order_addons WHERE order_id = $1 ORDER BY code;

-- name: InsertPayment :exec
INSERT INTO payments (order_id, provider, provider_order_id, amount_paise)
VALUES ($1, $2, $3, $4);

-- name: GetPaymentByOrderID :one
SELECT * FROM payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: LockPaymentByProviderOrderID :one
SELECT * FROM payments WHERE provider = $1 AND provider_order_id = $2 FOR UPDATE;

-- name: UpdatePaymentStatus :exec
UPDATE payments SET status = $2, provider_payment_id = $3, updated_at = now() WHERE id = $1;

-- name: UpdateOrderPaymentState :exec
UPDATE orders SET status = $2, payment_status = $3, updated_at = now() WHERE id = $1;

-- name: InsertPaymentEvent :execrows
INSERT INTO payment_events (provider, event_id, payload) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: InsertNewsletterSubscriber :exec
INSERT INTO newsletter_subscribers (email) VALUES (lower(sqlc.arg(email)::text))
ON CONFLICT DO NOTHING;

-- Unpaid orders whose reservation window has lapsed. SKIP LOCKED lets concurrent sweepers (multiple
-- instances) and in-flight webhooks proceed without blocking each other.
-- name: LockExpiredPendingOrders :many
SELECT id FROM orders
WHERE status = 'pending_payment' AND created_at < now() - make_interval(mins => sqlc.arg(ttl_minutes)::int)
ORDER BY created_at
LIMIT 100
FOR UPDATE SKIP LOCKED;

-- name: ExpireOrderPayments :exec
UPDATE payments SET status = 'expired', updated_at = now() WHERE order_id = $1 AND status = 'created';
