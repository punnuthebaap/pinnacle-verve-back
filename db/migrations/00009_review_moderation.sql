-- +goose Up
-- Review moderation (A5): who decided and when; pending reviews are what moderators look at first.
ALTER TABLE product_reviews ADD COLUMN moderated_at timestamptz;
ALTER TABLE product_reviews ADD COLUMN moderated_by uuid REFERENCES admin_users(id) ON DELETE SET NULL;
CREATE INDEX product_reviews_status_idx ON product_reviews (status, created_at DESC);

-- +goose Down
DROP INDEX product_reviews_status_idx;
ALTER TABLE product_reviews DROP COLUMN moderated_by;
ALTER TABLE product_reviews DROP COLUMN moderated_at;
