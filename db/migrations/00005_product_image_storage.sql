-- +goose Up
-- Uploaded images remember their storage key so deleting the row can delete the file. Fixture images
-- (served by the storefront from /images/…) have an empty key and are never touched.
ALTER TABLE product_images ADD COLUMN storage_key text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE product_images DROP COLUMN storage_key;
