# Pinnacle Verve — API

Go modular monolith serving the storefront ([`pinnacle-verve`](https://github.com/punnuthebaap/pinnacle-verve)). Contract: [`openapi/openapi.yaml`](openapi/openapi.yaml).

## Prerequisites

- Go 1.26+ (gcc needed only to build `sqlc` via `go tool`)
- PostgreSQL 17 on port 5433 — either `docker compose up -d` or, on Windows without Docker, the local cluster below

## Setup

```bash
cp .env.example .env            # optional; defaults match local development
docker compose up -d            # or: Windows native cluster (see below)
go run ./cmd/seed               # migrate + load development fixtures (RESETS data)
go run ./cmd/server             # http://localhost:8080/api/v1/health
```

Create the first admin owner (once; there are no default credentials):

```bash
ADMIN_PASSWORD='choose-12+-chars' go run ./cmd/admin create-owner -email you@example.com -name "Your Name"

# Local development / E2E only (refused unless APP_ENV=development): create or reset a known owner
ADMIN_PASSWORD='e2e-owner-password' go run ./cmd/admin create-owner -email e2e-owner@example.com -name "E2E Owner" -dev-reset
```

Windows without Docker (uses installed PostgreSQL binaries; data in `.local/pgdata`, git-ignored):

```bash
B="/c/Program Files/PostgreSQL/17/bin"
"$B/initdb.exe" -D .local/pgdata -U postgres --auth=trust -E UTF8 --locale=C   # first time only
"$B/pg_ctl.exe" -D .local/pgdata -o "-p 5433" -l .local/postgres.log -w start
"$B/createdb.exe" -h localhost -p 5433 -U postgres pinnacle                    # first time only
```

## Commands

| Command | Purpose |
|---|---|
| `go test ./...` | Unit + integration tests (integration uses `pinnacle_test` DB; skipped if no DB) |
| `go generate ./...` | Regenerate server types from OpenAPI |
| `go tool sqlc generate` | Regenerate typed queries from `db/queries` |
| `go run ./cmd/seed` | Reset and load fixtures (refuses in production) |

## Configuration

All via environment; see `.env.example`. Production requires `PAYMENT_PROVIDER≠mock` and `COOKIE_SECURE=true`. Unconfirmed commercial values (white-glove threshold, shipping, add-on GST rate, enabled payment methods) are env-configurable.

## Docs

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) · [docs/API.md](docs/API.md) · system decisions and roadmap in the frontend repo's `docs/`.

## Troubleshooting

- **`ping database` error** — Postgres isn't on 5433 or `DATABASE_URL` is wrong.
- **403 `forbidden_origin`** — add the site origin to `ALLOWED_ORIGINS`.
- **CI fails on "generated code is up to date"** — run `go generate ./... && go tool sqlc generate` and commit.
- **409 `out_of_stock` in tests/E2E** — reseed; fixtures have small stock counts on purpose.
