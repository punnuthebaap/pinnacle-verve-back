-- +goose Up
-- Admin order operations (A3): who performed each order event, shipment tracking shown to the shopper,
-- and a single definition of "needs attention" used by the list, the counts and the detail screen.

ALTER TABLE order_events ADD COLUMN admin_id uuid REFERENCES admin_users(id) ON DELETE SET NULL;
ALTER TABLE orders ADD COLUMN tracking_carrier text NOT NULL DEFAULT '' CHECK (length(tracking_carrier) <= 80);
ALTER TABLE orders ADD COLUMN tracking_number  text NOT NULL DEFAULT '' CHECK (length(tracking_number) <= 80);
CREATE INDEX orders_created_idx ON orders (created_at DESC);

-- Money was taken but the order isn't going ahead, so a refund must be issued and recorded:
--   refund_due      paid order that was cancelled
--   late_capture    paid after the reservation lapsed (stock was released); reinstate or refund
--   amount_mismatch the gateway captured a different amount than the order total
CREATE VIEW order_attention AS
SELECT o.id AS order_id,
       (CASE
            WHEN o.payment_status = 'refunded' THEN ''
            WHEN o.status = 'cancelled' AND o.payment_status = 'paid' THEN 'refund_due'
            WHEN o.status = 'cancelled' AND EXISTS (
                SELECT 1 FROM order_events e WHERE e.order_id = o.id AND e.type = 'payment_captured_after_expiry')
                THEN 'late_capture'
            WHEN o.status <> 'placed' AND EXISTS (
                SELECT 1 FROM order_events e WHERE e.order_id = o.id AND e.type = 'payment_amount_mismatch')
                THEN 'amount_mismatch'
            ELSE ''
        END)::text AS reason
FROM orders o;

-- +goose Down
DROP VIEW order_attention;
DROP INDEX orders_created_idx;
ALTER TABLE orders DROP COLUMN tracking_number;
ALTER TABLE orders DROP COLUMN tracking_carrier;
ALTER TABLE order_events DROP COLUMN admin_id;
