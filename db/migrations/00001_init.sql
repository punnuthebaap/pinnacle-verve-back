-- +goose Up
-- Money columns are integer paise, GST-inclusive.

CREATE TABLE categories (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    eyebrow     text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    position    int  NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT true
);

-- Facets shown on a category's listing page; values come from product_attributes.
CREATE TABLE category_facets (
    category_id uuid NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    key         text NOT NULL,
    label       text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('multi', 'single', 'swatch')),
    position    int  NOT NULL DEFAULT 0,
    PRIMARY KEY (category_id, key)
);

CREATE TABLE products (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug           text NOT NULL UNIQUE,
    category_id    uuid NOT NULL REFERENCES categories(id),
    name           text NOT NULL,
    subtitle       text NOT NULL DEFAULT '',
    series         text NOT NULL DEFAULT '',
    material_label text NOT NULL DEFAULT '',
    size_label     text NOT NULL DEFAULT '',
    description    text NOT NULL DEFAULT '',
    shipping_note  text NOT NULL DEFAULT '',
    badges         jsonb NOT NULL DEFAULT '[]',   -- [{label, tone}]
    features       jsonb NOT NULL DEFAULT '[]',   -- [{title, body}]
    options        jsonb NOT NULL DEFAULT '[]',   -- [{code, name, kind, values:[{code,label,detail,swatchHex}]}]
    gst_rate_bps   int  NOT NULL DEFAULT 1800 CHECK (gst_rate_bps BETWEEN 0 AND 2800),
    rating_avg     numeric(2,1) NOT NULL DEFAULT 0,
    rating_count   int  NOT NULL DEFAULT 0,
    rank           int  NOT NULL DEFAULT 0,       -- "featured" sort order
    is_active      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX products_category_idx ON products (category_id) WHERE is_active;

CREATE TABLE product_images (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    url        text NOT NULL,
    alt        text NOT NULL,
    position   int  NOT NULL DEFAULT 0
);
CREATE INDEX product_images_product_idx ON product_images (product_id, position);

CREATE TABLE product_variants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    sku         text NOT NULL UNIQUE,
    options     jsonb NOT NULL DEFAULT '{}',   -- {optionCode: valueCode}
    price_paise bigint NOT NULL CHECK (price_paise > 0),
    mrp_paise   bigint NOT NULL CHECK (mrp_paise >= price_paise),
    stock       int NOT NULL DEFAULT 0 CHECK (stock >= 0),
    position    int NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT true
);
CREATE INDEX product_variants_product_idx ON product_variants (product_id);

CREATE TABLE product_attributes (
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    key        text NOT NULL,
    value      text NOT NULL,
    label      text NOT NULL,
    swatch_hex text NOT NULL DEFAULT '',
    PRIMARY KEY (product_id, key, value)
);
CREATE INDEX product_attributes_kv_idx ON product_attributes (key, value);

CREATE TABLE addons (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    price_paise bigint NOT NULL CHECK (price_paise >= 0),
    is_active   boolean NOT NULL DEFAULT true,
    position    int NOT NULL DEFAULT 0
);

-- Anonymous or authenticated browser session. id is SHA-256 of the cookie token.
CREATE TABLE sessions (
    id          bytea PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE TABLE carts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  bytea NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
    addon_codes text[] NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cart_items (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id    uuid NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
    variant_id uuid NOT NULL REFERENCES product_variants(id),
    quantity   int  NOT NULL CHECK (quantity BETWEEN 1 AND 10),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cart_id, variant_id)
);

CREATE TABLE orders (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ref               text NOT NULL UNIQUE,
    session_id        bytea REFERENCES sessions(id) ON DELETE SET NULL,
    idempotency_key   text NOT NULL,
    email             text NOT NULL,
    shipping_address  jsonb NOT NULL,
    gstin             text NOT NULL DEFAULT '',
    notes             text NOT NULL DEFAULT '',
    payment_method    text NOT NULL,
    status            text NOT NULL DEFAULT 'pending_payment'
                      CHECK (status IN ('pending_payment', 'placed', 'cancelled')),
    payment_status    text NOT NULL DEFAULT 'pending'
                      CHECK (payment_status IN ('pending', 'paid', 'failed', 'refunded')),
    fulfilment_status text NOT NULL DEFAULT 'unfulfilled'
                      CHECK (fulfilment_status IN ('unfulfilled', 'preparing', 'in_transit', 'delivered', 'installed')),
    subtotal_paise    bigint NOT NULL,
    discount_paise    bigint NOT NULL DEFAULT 0,
    shipping_paise    bigint NOT NULL DEFAULT 0,
    addons_paise      bigint NOT NULL DEFAULT 0,
    tax_paise         bigint NOT NULL,
    total_paise       bigint NOT NULL CHECK (total_paise >= 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, idempotency_key)
);

CREATE TABLE order_items (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    variant_id       uuid NOT NULL REFERENCES product_variants(id),
    product_slug     text NOT NULL,
    product_name     text NOT NULL,
    variant_label    text NOT NULL,
    sku              text NOT NULL,
    image_url        text NOT NULL,
    image_alt        text NOT NULL,
    unit_price_paise bigint NOT NULL,
    quantity         int NOT NULL CHECK (quantity > 0),
    line_total_paise bigint NOT NULL
);
CREATE INDEX order_items_order_idx ON order_items (order_id);

CREATE TABLE order_addons (
    order_id    uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    code        text NOT NULL,
    name        text NOT NULL,
    description text NOT NULL,
    price_paise bigint NOT NULL,
    PRIMARY KEY (order_id, code)
);

CREATE TABLE order_events (
    id         bigserial PRIMARY KEY,
    order_id   uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    type       text NOT NULL,
    data       jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_events_order_idx ON order_events (order_id, id);

CREATE TABLE payments (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    provider          text NOT NULL,
    provider_order_id text NOT NULL,
    provider_payment_id text NOT NULL DEFAULT '',
    amount_paise      bigint NOT NULL,
    status            text NOT NULL DEFAULT 'created'
                      CHECK (status IN ('created', 'captured', 'failed')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_order_id)
);

-- Dedupe store for gateway webhooks (at-least-once delivery).
CREATE TABLE payment_events (
    provider    text NOT NULL,
    event_id    text NOT NULL,
    payload     jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, event_id)
);

CREATE TABLE newsletter_subscribers (
    email      text PRIMARY KEY,   -- stored lower-cased
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE newsletter_subscribers, payment_events, payments, order_events, order_addons,
    order_items, orders, cart_items, carts, sessions, addons, product_attributes,
    product_variants, product_images, products, category_facets, categories;
