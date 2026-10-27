-- +goose Up
-- Rich product content for the product page, reviews (single source of rating aggregates) and
-- curated "pairs well with" relations.

ALTER TABLE products
    ADD COLUMN specs jsonb NOT NULL DEFAULT '[]', -- [{title, body, bullets[]}] accordion sections
    ADD COLUMN care  jsonb NOT NULL DEFAULT '[]', -- [{eyebrow, title, body}]
    ADD COLUMN facts jsonb NOT NULL DEFAULT '[]'; -- [{label, value}] e.g. depth, weight

CREATE TABLE product_reviews (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id    uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    rating        smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    author_name   text NOT NULL DEFAULT '',
    author_meta   text NOT NULL DEFAULT '',  -- e.g. "Principal Architect, Delhi NCR"
    body          text NOT NULL DEFAULT '',  -- empty = rating-only
    variant_label text NOT NULL DEFAULT '',
    verified      boolean NOT NULL DEFAULT false,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'rejected')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX product_reviews_product_idx ON product_reviews (product_id, created_at DESC) WHERE status = 'published';

CREATE TABLE product_relations (
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    related_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    position   int  NOT NULL DEFAULT 0,
    PRIMARY KEY (product_id, related_id),
    CHECK (product_id <> related_id)
);

-- +goose Down
DROP TABLE product_relations, product_reviews;
ALTER TABLE products DROP COLUMN specs, DROP COLUMN care, DROP COLUMN facts;
