# Backend architecture

System overview and decision log: `pinnacle-verve/docs/ARCHITECTURE.md` (frontend repo). This file covers backend internals.

## Layout

```
cmd/server            HTTP entrypoint (graceful shutdown, auto-migrate in dev)
cmd/seed              loads db/seed/dev.sql fixtures (refuses in production)
db/migrations         goose SQL migrations (embedded)
db/queries            SQL for sqlc → internal/store (generated, do not edit)
openapi/openapi.yaml  contract → internal/api/api.gen.go (generated, do not edit)
internal/app          wiring: services + middleware stack (shared with integration test)
internal/httpapi      handlers: implement api.StrictServerInterface; map transport ↔ service types only
internal/catalog      categories, facets, products (read model)
internal/cart         session cart; pricing via pricing.Compute
internal/order        cart → order, stock reservation, payment webhooks
internal/payment      Provider interface; Mock (HMAC-signed webhooks); Razorpay later
internal/pricing      pure totals/GST math (unit tested)
internal/platform     config, database (pool, migrate, InTx), httpx (middleware, problems), session
```

Dependency direction: `httpapi → services (catalog|cart|order) → store`. Services never import `api`. `pricing` and `payment` have no DB access.

## Request pipeline

`Observe` (request id, log, recover) → `Harden` (headers, 1 MiB body cap) → `SameOrigin` (CSRF) → `session` → `CaptureWebhook` (raw body for signatures) → OpenAPI validator → generated router → handler.

## Data model (migration 00001)

- Catalogue: `categories`, `category_facets` (which filters a category shows), `products` (badges/features/options as JSONB), `product_variants` (SKU, price, MRP, stock, option map), `product_images`, `product_attributes` (facet values).
- Commerce: `sessions` (hashed token), `carts`, `cart_items`, `addons`, `orders` (status / payment_status / fulfilment_status kept separate; totals snapshotted), `order_items` (price/name snapshot), `order_addons`, `order_events` (audit timeline), `payments`, `payment_events` (webhook de-dupe).
- The display price of a product is its first variant by `position`.

## Integrity rules

- Prices are read from variants at order time and snapshotted; client totals are never accepted.
- Stock is reserved with a conditional `UPDATE … WHERE stock >= qty` inside the order transaction; released when payment fails.
- Order creation is idempotent per (session, Idempotency-Key). Webhooks are idempotent per (provider, event id) and lock the payment row.
- Payment amount mismatch on capture is recorded and left for manual review (never auto-accepted).

## Known gaps (tracked in HANDOFF)

Pending-order expiry (release reserved stock), gateway call outside the DB transaction, rate limiting, coupons, CGST/SGST split.
