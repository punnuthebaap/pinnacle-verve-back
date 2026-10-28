# Handoff — backend

_Last updated: 2026-10-28 · API PRs #1–#15 merged into `main` · Next: owner acceptance testing locally, then deploy (B9)_

New developers: setup and testing guides live in the storefront repo, `../pinnacle-verve/docs/GETTING_STARTED.md` and `docs/TESTING.md`.

## Done (M0 + M1)

- OpenAPI 3.0 contract: health, categories (+facets with counts), products (filters, sort, pagination), product detail (options/variants), cart (items, add-ons), add-ons, orders (idempotent create, session-scoped read), mock payment completion, signed webhooks, newsletter.
- Migration `00001_init.sql` (catalogue, sessions, carts, orders, payments, webhook de-dupe, newsletter).
- Services: catalog, cart (stock/limit checks), order (transactional create, stock reservation, payment capture/failure, cart clear), pricing (GST-inclusive math), payment (provider interface + HMAC mock).
- Middleware: request id + JSON logs + panic recovery, security headers, 1 MiB body cap, CSRF origin check, lazy session cookie, raw-body capture for webhooks, OpenAPI request validation.
- Dev fixtures (`db/seed/dev.sql`): 2 categories, 13 products, 24-variant configurable Contour mirror, facets, 2 add-ons.
- CI workflow: generated-code drift check, vet, race tests against Postgres service.

## Validation

- 2026-10-28, merged `main` in a fresh clone: `go vet ./...` clean, `go test ./...` pass; storefront Playwright 33/33 against it; seed + `cmd/admin create-owner` + `cmd/server` as in GETTING_STARTED.

Earlier (2026-10-27):

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
- ✅ A4 settings: `internal/settings`, `store_settings` row (migration 00007) replacing env values at runtime, add-on editing, public `/checkout-options`; cart/order/sweeper read settings per request. Test `TestAdminSettings`.
- ✅ Contact WhatsApp number as a setting (migration 00008, default `+918084047949` confirmed by the owner 2026-10-28), public `/contact`, settings changes revalidate storefront tag `settings`. Cash on delivery removed from the API.
- ✅ A5: `internal/marketing`, review moderation with rating recompute (migration 00009: moderated_at/by), newsletter list, CSV export (formula-safe, audited) and removal. Test `TestAdminMarketing`. Tests reset staff with `resetAdmins` (no TRUNCATE CASCADE: it would wipe reviews/order events that reference admins).
- ✅ A6: `internal/content` (migration 00010: content_documents, content_pages), public content endpoints, link allow-list. Tests `TestAdminContent`, `TestCheckLinks`. The admin panel phases A1–A6 are complete.

## M7 hardening

- ✅ Per-IP rate limits (`internal/platform/ratelimit`): sign-in 20/min, newsletter 10/min, orders 20/min, mock payments 30/min, cart writes 120/min, image uploads 120/min → 429 + Retry-After. `RATE_LIMIT_ENABLED`, `TRUSTED_PROXIES`. Tests `TestLimiterWindows`, `TestClientIP`, `TestRateLimits`.
- ✅ One lock order everywhere (order row, then payment rows): webhooks now match the expiry sweeper and admin actions, so a webhook racing a staff cancellation queues instead of deadlocking. Test `TestWebhookAndAdminCancelRace` (fails with a deadlock 500 on the old order).
- ✅ Gateway order created **after** the order transaction commits (only the order row is locked during the call); on failure the order is removed and stock released → `503 payment_gateway_unavailable`, and the same checkout can be retried. Test `TestGatewayFailureAtCheckout`.

## Known issues / gaps

- Admin sign-in has no 2FA (owner decision); it is throttled per IP (20/min) on top of the per-account lockout.
- **Deployment requirement**: per-IP limits key on X-Forwarded-For, trusted only from `TRUSTED_PROXIES`. The storefront forwards a client-supplied X-Forwarded-For unchanged, so put a load balancer/edge proxy in front that appends the real client address (standard behaviour), and add the storefront's address to `TRUSTED_PROXIES`. Limits are per instance (in memory); use a shared store when running several instances (B9).
- Public review submission not built: who may review (anyone, moderated; or verified buyers only) is an owner decision (M5). Moderation is ready for it.
- Contour spec accordion: only "Materials & Glass Provenance" has copy; the other three sections are collapsed in Figma (B6).
- Refunds are recorded, not issued: staff refund in the gateway dashboard and enter the reference (full refunds only; partial refunds/returns need business rules). Automate with Razorpay (B1).
- No customer emails on dispatch/cancellation yet (needs an email provider and approved copy).
- Discounts/coupons not implemented; GST is one "included" figure (CGST/SGST split blocked by B3).
- `ListProducts` returns `total=0` when the requested page is beyond the last page (window count).

## Next steps (in order)

1. S3/R2 `storage.Store` implementation and a shared rate-limit store when hosting is chosen (B9).
2. Razorpay provider (when B1 credentials arrive): `payment.Razorpay` implementing `Provider`, webhook signature `X-Razorpay-Signature`.
