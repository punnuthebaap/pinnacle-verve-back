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
internal/storage      Store interface for uploaded media; Local (disk + /media handler) until hosting is chosen
internal/settings     store settings (commercial values, add-ons): read per request by cart/order, edited by owners with audit
internal/orderadmin   admin order operations: queues/search, fulfilment, cancel, refund records, late-capture reinstatement, notes (order event + audit in one transaction)
internal/catalogadmin admin catalogue writes: validation, audit-in-transaction, storefront revalidation (HTTPRevalidator)
internal/admin        staff accounts: argon2id passwords, lockout, sessions, RBAC (Can(role, area)), audit log
cmd/admin             bootstrap the first owner (password from ADMIN_PASSWORD)
internal/platform     config, database (pool, migrate, InTx), httpx (middleware, problems), session
```

Dependency direction: `httpapi → services (catalog|cart|order) → store`. Services never import `api`. `pricing` and `payment` have no DB access.

## Request pipeline

`Observe` (request id, log, recover) → `Harden` (headers, 1 MiB body cap) → `SameOrigin` (CSRF) → `session` → `admin` (staff auth for `/api/v1/admin/*`) → `CaptureWebhook` (raw body for signatures) → OpenAPI validator → generated router → handler.

## Data model (migration 00001)

- Catalogue: `categories`, `category_facets` (which filters a category shows), `products` (badges/features/options as JSONB), `product_variants` (SKU, price, MRP, stock, option map), `product_images`, `product_attributes` (facet values).
- Commerce: `sessions` (hashed token), `carts`, `cart_items`, `addons`, `orders` (status / payment_status / fulfilment_status kept separate; totals snapshotted), `order_items` (price/name snapshot), `order_addons`, `order_events` (audit timeline), `payments`, `payment_events` (webhook de-dupe).
- Product-page content (migration 00002): `products.specs` / `care` / `facts` JSONB; `product_reviews` (rating-only rows allowed; `status` moderation); `product_relations` ("pairs well with").
- `products.rating_avg` / `rating_count` are derived from published reviews — recompute them whenever reviews change. The reviews endpoint computes its summary from the same rows.
- The display price of a product is its first variant by `position`.

## Integrity rules

- Prices are read from variants at order time and snapshotted; client totals are never accepted.
- Stock is reserved with a conditional `UPDATE … WHERE stock >= qty` inside the order transaction; released when payment fails.
- Order creation is idempotent per (session, Idempotency-Key). Webhooks are idempotent per (provider, event id) and lock the payment row.
- Payment amount mismatch on capture is recorded and left for manual review (never auto-accepted).
- Commercial values (white-glove threshold, shipping, add-on GST, payment methods, unpaid-order hold time) live in the single-row `store_settings` table (migration 00007), created from the env defaults on first use and edited by owners in the admin. Cart pricing, checkout and the expiry sweeper read the row per request/tick, so every instance applies a change at once; order creation prices with the same values the cart showed.
- Unpaid orders expire after the configured hold time (default 30 min): a per-minute sweeper claims them with `SKIP LOCKED`, releases stock and sets `cancelled` / `expired`. A capture that arrives after expiry is recorded as `payment_captured_after_expiry` for a manual refund — never auto-accepted.
- Cart `notes` (installation instructions) are copied into the order when checkout sends none, and cleared with the cart after payment.

## Known gaps (tracked in HANDOFF)

Gateway call outside the DB transaction, rate limiting, coupons, CGST/SGST split.
