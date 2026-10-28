# Handoff — backend

_Last updated: 2026-10-27 · Current milestone: M4_

## Done (M0 + M1)

- OpenAPI 3.0 contract: health, categories (+facets with counts), products (filters, sort, pagination), product detail (options/variants), cart (items, add-ons), add-ons, orders (idempotent create, session-scoped read), mock payment completion, signed webhooks, newsletter.
- Migration `00001_init.sql` (catalogue, sessions, carts, orders, payments, webhook de-dupe, newsletter).
- Services: catalog, cart (stock/limit checks), order (transactional create, stock reservation, payment capture/failure, cart clear), pricing (GST-inclusive math), payment (provider interface + HMAC mock).
- Middleware: request id + JSON logs + panic recovery, security headers, 1 MiB body cap, CSRF origin check, lazy session cookie, raw-body capture for webhooks, OpenAPI request validation.
- Dev fixtures (`db/seed/dev.sql`): 2 categories, 13 products, 24-variant configurable Contour mirror, facets, 2 add-ons.
- CI workflow: generated-code drift check, vet, race tests against Postgres service.

## Validation (2026-10-27)

- `go build ./...`, `go vet ./...`: clean.
- `go test ./...`: pass — pricing, payment signature, variant label, and `TestPurchaseJourney` (real DB: filters, 404s, CSRF 403, validation 400, stock 409, add-on totals, idempotent order replay, forged webhook 401, mock capture, cart cleared, stock decremented, cross-session order 404).
- Exercised by frontend Playwright E2E (3/3 pass).

## M2 progress

- ✅ Product content API: `specs`/`care`/`facts` on product detail, `GET /products/{slug}/reviews` (summary + written reviews), `GET /products/{slug}/related`; migration 00002; ratings derived from reviews; integration test extended.

## M4 progress

- ✅ Cart notes (`PUT /cart/notes`, copied into the order), pending-order expiry sweeper with late-capture flagging (migration 00003). Integration test `TestPendingOrderExpiry`.

## Admin panel (decisions D32–D35)

- ✅ A1 backend: admin users/roles/sessions/audit (migration 00004), `/api/v1/admin` session + users + audit endpoints, `cmd/admin create-owner`. Test `TestAdminAuth`.
- ✅ A1 frontend (storefront PR #8).
- ✅ A2a catalogue API: categories + filters, products (all content fields), variants (validated options, SKU/combination uniqueness, deactivate-not-delete), filter values, pairings; audit in the same transaction; storefront revalidation after each write. Test `TestAdminCatalog`.
- ✅ A2b images: `internal/storage` (local disk, `MEDIA_DIR`), upload/reorder/alt/delete endpoints, `/media/*` serving, per-route body cap (migration 00005). Test `TestAdminImages`.
- ✅ A3 orders: `internal/orderadmin`, list/queues/search, detail + timeline, fulfilment with tracking (shown to shoppers), cancel, refund records, late-capture reinstatement, notes (migration 00006, view `order_attention`). Test `TestAdminOrders`.
- ⬜ A4 settings → A5 reviews/newsletter → A6 content.

## Known issues / gaps

- Admin sign-in has no 2FA (owner decision); add IP-based rate limiting for password spraying in M7.

- Review submission/moderation endpoints not built (M5, needs accounts or verified-purchase rule).
- Contour spec accordion: only "Materials & Glass Provenance" has copy; the other three sections are collapsed in Figma (B6).
- Refunds are recorded, not issued: staff refund in the gateway dashboard and enter the reference (full refunds only; partial refunds/returns need business rules). Automate with Razorpay (B1).
- No customer emails on dispatch/cancellation yet (needs an email provider and approved copy).
- Admin actions lock the order row before payment rows (same order as the expiry sweeper); the webhook path locks payments first, so a simultaneous webhook and admin action can deadlock — Postgres aborts one and it can be retried. Unify lock order with the Razorpay work.
- Gateway order creation runs inside the DB transaction (fine for mock; move after commit for Razorpay).
- No rate limiting yet (newsletter, cart, orders) — M7.
- Discounts/coupons not implemented; GST is one "included" figure (CGST/SGST split blocked by B3).
- `ListProducts` returns `total=0` when the requested page is beyond the last page (window count).

## Next steps (in order)

1. A4 settings: move commercial values (white-glove threshold, shipping, add-on GST, payment methods, pending TTL) from env to an audited settings table editable by owners.
2. S3/R2 `storage.Store` implementation when hosting is chosen (B9).
2. Move the gateway order call after commit (with retry) before Razorpay goes live.
3. Razorpay provider (when B1 credentials arrive): `payment.Razorpay` implementing `Provider`, webhook signature `X-Razorpay-Signature`.
