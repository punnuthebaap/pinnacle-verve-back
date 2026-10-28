# Pinnacle Verve — API

Go modular monolith serving the storefront ([`pinnacle-verve`](https://github.com/punnuthebaap/pinnacle-verve), checked out next to this repo). Contract: [`openapi/openapi.yaml`](openapi/openapi.yaml).

**New to the project?** The step-by-step setup and testing guides are in the storefront repo: [`docs/GETTING_STARTED.md`](https://github.com/punnuthebaap/pinnacle-verve/blob/main/docs/GETTING_STARTED.md) and [`docs/TESTING.md`](https://github.com/punnuthebaap/pinnacle-verve/blob/main/docs/TESTING.md).

## Prerequisites

- Go 1.26+ (gcc is only needed for `go test -race` and to build `sqlc` via `go tool`)
- PostgreSQL 17 on port **5433**: either `docker compose up -d`, or, on Windows without Docker, the local cluster below

## Setup

```bash
docker compose up -d            # or: Windows native cluster (see below)
go run ./cmd/seed               # migrate + load development fixtures (RESETS data)
go run ./cmd/server             # http://localhost:8080/api/v1/health
```

Configuration comes from environment variables, and the defaults match local development, so nothing needs setting. The server does **not** read a `.env` file; `.env.example` documents every variable. To override one, use `PORT=8081 go run ./cmd/server` (Git Bash) or `$env:PORT='8081'; go run ./cmd/server` (PowerShell).

Create the first admin owner (once; there are no default credentials):

```bash
ADMIN_PASSWORD='choose-12+-chars' go run ./cmd/admin create-owner -email you@example.com -name "Your Name"
# PowerShell: $env:ADMIN_PASSWORD='choose-12+-chars'; go run ./cmd/admin create-owner -email you@example.com -name "Your Name"

# Development only (refused unless APP_ENV=development): create or reset a known owner, which also clears a lockout
ADMIN_PASSWORD='e2e-owner-password' go run ./cmd/admin create-owner -email e2e-owner@example.com -name "E2E Owner" -dev-reset
```

Windows without Docker (uses installed PostgreSQL binaries; data in `.local/pgdata`, git-ignored; leaves a PostgreSQL service on 5432 alone):

```bash
B="/c/Program Files/PostgreSQL/17/bin"
"$B/initdb.exe" -D .local/pgdata -U postgres --auth=trust -E UTF8 --locale=C   # first time only
"$B/pg_ctl.exe" -D .local/pgdata -o "-p 5433" -l .local/postgres.log -w start  # after each reboot
"$B/createdb.exe" -h localhost -p 5433 -U postgres pinnacle                    # first time only
```

## Commands

| Command | Purpose |
|---|---|
| `go test ./...` | Unit + integration tests (integration uses the `pinnacle_test` DB on 5433, created automatically; skipped if no DB) |
| `go vet ./...` | Static checks (CI) |
| `go generate ./...` | Regenerate server types from OpenAPI |
| `go tool sqlc generate` | Regenerate typed queries from `db/queries` |
| `go run ./cmd/seed` | Reset and load fixtures: catalogue, carts, orders, newsletter, settings and content. Staff accounts are kept. Refuses in production |
| `go run ./cmd/admin create-owner …` | Bootstrap an owner (see above) |

CI (`.github/workflows/ci.yml`) checks generated code is up to date, then runs `go vet` and `go test -race` against PostgreSQL.

## Configuration

All via environment; see `.env.example`. Production (`APP_ENV=production`) refuses to start with `PAYMENT_PROVIDER=mock`, without `COOKIE_SECURE=true`, or with a weak `REVALIDATE_SECRET`. Commercial values in env (white-glove threshold, shipping, add-on GST rate, payment methods, WhatsApp number, unpaid-order hold) only create the initial settings row; after that owners edit them in Admin → Settings. When the storefront isn't on `http://localhost:3000`, set `ALLOWED_ORIGINS` and `STOREFRONT_REVALIDATE_URL` to its address.

## Docs

- [HANDOFF.md](HANDOFF.md): current state and next steps.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): backend internals.
- [docs/API.md](docs/API.md): API conventions and error codes.
- [AI_CONTEXT.md](AI_CONTEXT.md): conventions in brief.
- System decisions, roadmap, setup and testing live in the storefront repo's `docs/`.

## Troubleshooting

- **`ping database` error**: Postgres isn't on 5433, or `DATABASE_URL` is wrong.
- **403 `forbidden_origin`**: add the site origin to `ALLOWED_ORIGINS`.
- **Admin edits don't appear on the storefront**: `STOREFRONT_REVALIDATE_URL` must point at the running storefront, and `REVALIDATE_SECRET` must match in both repos.
- **429 `rate_limited` while testing by hand**: wait a minute, or run with `RATE_LIMIT_ENABLED=false`.
- **CI fails on "generated code is up to date"**: run `go generate ./... && go tool sqlc generate` and commit.
- **409 `out_of_stock` in tests/E2E**: reseed; fixtures have small stock counts on purpose.
