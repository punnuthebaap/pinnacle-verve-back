-- +goose Up
-- Commercial values editable by owners in the admin (A4). One row; created on first use from the
-- environment defaults (WHITE_GLOVE_THRESHOLD_PAISE, …), after which the database is the source of truth.

CREATE TABLE store_settings (
    id                          boolean PRIMARY KEY DEFAULT true CHECK (id),
    white_glove_threshold_paise bigint NOT NULL CHECK (white_glove_threshold_paise >= 0),
    shipping_paise              bigint NOT NULL CHECK (shipping_paise >= 0),
    addon_gst_rate_bps          int NOT NULL CHECK (addon_gst_rate_bps BETWEEN 0 AND 4000),
    payment_methods             text[] NOT NULL CHECK (cardinality(payment_methods) > 0),
    pending_order_ttl_minutes   int NOT NULL CHECK (pending_order_ttl_minutes BETWEEN 10 AND 1440),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    updated_by                  uuid REFERENCES admin_users(id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE store_settings;
