-- name: AdminListOrders :many
SELECT o.id, o.ref, o.created_at, o.email,
       (o.shipping_address->>'fullName')::text AS customer_name,
       (o.shipping_address->>'phone')::text AS phone,
       (o.shipping_address->>'city')::text AS city,
       o.status, o.payment_status, o.fulfilment_status, o.payment_method, o.total_paise,
       (SELECT coalesce(sum(i.quantity), 0) FROM order_items i WHERE i.order_id = o.id)::int AS item_count,
       a.reason AS attention,
       count(*) OVER ()::int AS total_count
FROM orders o
JOIN order_attention a ON a.order_id = o.id
WHERE (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status))
  AND (sqlc.narg(payment_status)::text IS NULL OR o.payment_status = sqlc.narg(payment_status))
  AND (sqlc.narg(fulfilment_status)::text IS NULL OR o.fulfilment_status = sqlc.narg(fulfilment_status))
  AND (sqlc.arg(queue)::text = ''
       OR (sqlc.arg(queue) = 'to_fulfil' AND o.status = 'placed' AND o.fulfilment_status IN ('unfulfilled', 'preparing'))
       OR (sqlc.arg(queue) = 'attention' AND a.reason <> ''))
  AND (sqlc.arg(q)::text = ''
       OR o.ref ILIKE '%' || sqlc.arg(q) || '%'
       OR o.email ILIKE '%' || sqlc.arg(q) || '%'
       OR (o.shipping_address->>'phone') LIKE '%' || sqlc.arg(q) || '%'
       OR (o.shipping_address->>'fullName') ILIKE '%' || sqlc.arg(q) || '%')
ORDER BY o.created_at DESC, o.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminOrderCounts :one
SELECT count(*) FILTER (WHERE o.status = 'placed' AND o.fulfilment_status IN ('unfulfilled', 'preparing'))::int AS to_fulfil,
       count(*) FILTER (WHERE a.reason <> '')::int AS attention
FROM orders o
JOIN order_attention a ON a.order_id = o.id;

-- name: AdminGetOrderIDByRef :one
SELECT id FROM orders WHERE ref = $1;

-- name: AdminLockOrder :one
SELECT * FROM orders WHERE id = $1 FOR UPDATE;

-- name: AdminOrderAttention :one
SELECT reason FROM order_attention WHERE order_id = $1;

-- name: AdminListOrderEvents :many
SELECT e.id, e.type, e.data, e.created_at, coalesce(u.name, '')::text AS admin_name
FROM order_events e
LEFT JOIN admin_users u ON u.id = e.admin_id
WHERE e.order_id = $1
ORDER BY e.id;

-- name: AdminLateCaptureEvent :one
SELECT data FROM order_events
WHERE order_id = $1 AND type = 'payment_captured_after_expiry'
ORDER BY id DESC LIMIT 1;

-- name: AdminInsertOrderEvent :exec
INSERT INTO order_events (order_id, type, data, admin_id) VALUES ($1, $2, $3, $4);

-- name: AdminSetFulfilment :exec
UPDATE orders SET fulfilment_status = $2, tracking_carrier = $3, tracking_number = $4, updated_at = now()
WHERE id = $1;

-- name: AdminSetPaymentStatus :exec
UPDATE orders SET payment_status = $2, updated_at = now() WHERE id = $1;
