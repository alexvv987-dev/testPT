# Repository guidelines

## Project

This repository contains a Go URL shortener backed by PostgreSQL. Keep the
implementation intentionally small, explicit, and suitable for a security-
focused backend code review.

## Architecture

- `cmd/` contains executable wiring only.
- `internal/shortener/` owns validation, code generation, and use cases.
- `internal/store/` owns persistence and PostgreSQL-specific behavior.
- `internal/httpapi/` owns HTTP contracts and middleware.
- Do not add authentication, external URL fetching, or URL reputation calls.

## Development rules

- Format Go code with `gofmt`.
- Wrap errors with useful operation context without exposing secrets or URLs.
- Do not log request bodies, original URLs, query strings, credentials, or
  database connection strings.
- Keep interfaces at consumer boundaries and inject generators and storage so
  collision and failure paths remain testable.
- Preserve exact input URLs in storage; validation must not rewrite them.
- All commits created for this assignment must start with `feat:`.

## Verification

Run the following checks after relevant changes:

```text
go test ./...
go test -race ./...
go vet ./...
docker compose config
docker compose up --build
```

Integration tests require `TEST_DATABASE_URL`. Never commit real credentials or
local `.env` files.
