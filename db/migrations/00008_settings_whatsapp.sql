-- +goose Up
-- Contact WhatsApp number for storefront CTAs (E.164, e.g. +918084047949; empty hides the CTAs).
-- Existing rows get the business number the owner confirmed on 2026-10-28 (ROADMAP B11).
ALTER TABLE store_settings ADD COLUMN whatsapp_number text NOT NULL DEFAULT ''
    CHECK (whatsapp_number ~ '^(\+[1-9][0-9]{7,14})?$');
UPDATE store_settings SET whatsapp_number = '+918084047949';

-- +goose Down
ALTER TABLE store_settings DROP COLUMN whatsapp_number;
