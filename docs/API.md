# API

Source of truth: [`openapi/openapi.yaml`](../openapi/openapi.yaml). This page covers conventions only; never duplicate endpoint details here.

## Conventions

- Base path `/api/v1`. JSON in and out; field names camelCase.
- **Money** is integer paise (`…Paise`), GST-inclusive. The client never sends prices or totals.
- **Validation**: every request is validated against the spec before reaching a handler (kin-openapi). Unknown body fields are rejected (`additionalProperties: false`).
- **Errors**: RFC 9457 `application/problem+json` with a stable `code`:

| Status | code | Meaning |
|---|---|---|
| 400 | `validation_failed`, `bad_request` | Request doesn't match the contract |
| 401 | `invalid_signature` | Webhook signature check failed |
| 403 | `forbidden_origin` | Cross-site state-changing request |
| 404 | `not_found` | Unknown resource, or order not owned by this session |
| 409 | `out_of_stock`, `item_unavailable`, `duplicate_request` | Retry after refreshing state |
| 422 | `quantity_limit`, `unknown_addon`, `empty_cart`, `payment_method_unavailable` | Business rule rejected |
| 500/503 | `internal`, `unavailable` | Server fault (logged with `X-Request-Id`) |

## Sessions and security

- Cart and guest orders belong to the `pv_sid` cookie (httpOnly, SameSite=Lax, Secure in production). It is issued only when a handler needs it (first cart write).
- POST/PUT/PATCH/DELETE with a foreign `Origin` or `Sec-Fetch-Site: cross-site` are rejected (CSRF). The frontend proxies `/api/*` so requests are same-origin.
- `POST /orders` requires an `Idempotency-Key` header (16–64 chars); a replay returns the original order.
- Webhooks are authenticated by provider signature over the raw body, then de-duplicated by event id.

## Admin API (`/api/v1/admin/*`)

- Separate staff session: `POST /admin/session` sets `pv_admin` (httpOnly, `SameSite=Strict`, path `/api/v1/admin`, 12h absolute / 2h idle). Every other admin route returns `401 unauthenticated` without it; responses are `Cache-Control: no-store`.
- Passwords: argon2id; ≥ 12 characters. 5 consecutive failures lock the account for 15 minutes (`429 account_locked`); unknown emails get the same `401 invalid_credentials` as wrong passwords.
- Roles: `owner` (everything incl. users, settings, audit), `catalog`, `orders`, `content` (own area only) → `403 forbidden` otherwise. The last active owner can't be demoted or disabled (`409 last_owner`).
- Every sign-in attempt and change is written to the audit log (`GET /admin/audit`, owner only).
- **Catalogue** (`catalog` role): `/admin/categories` (+ `/facets`), `/admin/products` (search by name/slug/SKU, includes inactive), `/admin/products/{id}` and its `/variants`, `/attributes`, `/related`. Collection endpoints are full replacements (`PUT`); every response returns the whole product. Variants and products are deactivated, never deleted (orders reference them). Business-rule failures are `422 invalid` with a message meant for the admin; `409 slug_taken` / `sku_taken`.
- **Images**: `POST /admin/products/{id}/images?alt=…` takes the raw file as the body (`application/octet-stream`; JPEG/PNG/WebP detected from the bytes, ≤ 10 MB, ≤ 12 per product) → `413 image_too_large` / `422 invalid`. `PUT …/images` reorders and edits alt text (must list every image once; first = listing image). `DELETE …/images/{imageId}` also deletes the stored file. Files are served at `/media/…` with immutable caching.
- Bodies are capped at 1 MiB (10 MiB for image uploads) → `413 payload_too_large`.
- After every successful catalogue write the API calls `STOREFRONT_REVALIDATE_URL` (header `X-Revalidate-Secret`) so cached storefront pages refresh immediately.
- **Orders** (role `orders`): `GET /admin/orders?q&queue&status&paymentStatus&fulfilmentStatus&page` (newest first; `q` matches ref, email, phone or name; `queue=to_fulfil|attention`; response includes queue `counts`). `GET /admin/orders/{ref}` returns the order, payment, `attention` and the timeline (events carry `adminName` when staff made them). Actions, each returning the updated order or `422 invalid` with a message:
  - `POST …/fulfilment {status, carrier?, trackingNumber?, note?}`: paid orders only; forward only (steps may be skipped); tracking from `in_transit` on, and can be corrected later. Shoppers see `tracking` on their order.
  - `POST …/cancel {reason}`: not once dispatched. Releases stock. Unpaid → payment `expired` (a payment that still arrives is flagged as a late capture). Paid → `attention: refund_due`.
  - `POST …/refund {reference, note?}`: records a **full** refund made in the gateway dashboard; only when the order needs one (`attention` set).
  - `POST …/reinstate`: accepts a late capture (`attention: late_capture`) by re-reserving stock, all or nothing.
  - `POST …/notes {text}`: internal timeline note.
  - `attention` (view `order_attention`): `refund_due` | `late_capture` | `amount_mismatch` — money was taken but the order isn't going ahead.
- **Settings** (owners): `GET/PUT /admin/settings` (white-glove threshold, shipping, add-on GST in bps, payment methods from `supportedPaymentMethods`, unpaid-order hold 10–1440 min, contact `whatsappNumber` normalised to E.164 such as `+918084047949`, empty hides WhatsApp buttons; audited before/after; the storefront's `settings` cache tag is revalidated). Cash on delivery is not a payment method (removed 2026-10-28). `GET /admin/addons`, `PUT /admin/addons/{code}` creates or updates an add-on (codes permanent; switch off instead of deleting). Changes apply to open carts and new orders immediately.
- Public `GET /contact` → `whatsappNumber` (absent when empty).
- Public `GET /checkout-options` → enabled `paymentMethods` and `whiteGloveThresholdPaise` for the checkout page.

## Payment flow (M1: mock provider)

1. `POST /orders` → order `pending_payment`, stock reserved, `payment.providerOrderId` returned.
2. Client pays with the gateway (mock: `POST /payments/mock/{providerOrderId}/complete`, development only).
3. Gateway → `POST /payments/webhooks/{provider}` → order `placed`/`paid` and cart cleared, or `cancelled`/`failed` and stock released.
   Unpaid for longer than the reservation window → `cancelled`/`expired` and stock released; a later capture is flagged for manual refund.
4. Client polls `GET /orders/{ref}` until `paymentStatus` is not `pending`.

## Changing the contract

Edit `openapi/openapi.yaml` → `go generate ./...` here → `npm run gen:api` in the frontend repo. CI fails if generated code is stale.
