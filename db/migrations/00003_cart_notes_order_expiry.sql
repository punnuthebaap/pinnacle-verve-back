-- +goose Up
-- Installation notes captured on the cart page, and an explicit "expired" outcome for unpaid orders
-- whose reservation window lapsed (distinct from a gateway-reported failure).

ALTER TABLE carts ADD COLUMN notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 1000);

ALTER TABLE orders DROP CONSTRAINT orders_payment_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_payment_status_check
    CHECK (payment_status IN ('pending', 'paid', 'failed', 'expired', 'refunded'));

ALTER TABLE payments DROP CONSTRAINT payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
    CHECK (status IN ('created', 'captured', 'failed', 'expired'));

CREATE INDEX orders_pending_created_idx ON orders (created_at) WHERE status = 'pending_payment';

-- +goose Down
DROP INDEX orders_pending_created_idx;
ALTER TABLE payments DROP CONSTRAINT payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check CHECK (status IN ('created', 'captured', 'failed'));
ALTER TABLE orders DROP CONSTRAINT orders_payment_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_payment_status_check
    CHECK (payment_status IN ('pending', 'paid', 'failed', 'refunded'));
ALTER TABLE carts DROP COLUMN notes;
