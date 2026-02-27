---
title: Runtime Reference
description: Commands, environment variables, and key runtime endpoints.
---

## CLI commands

From `src/`:

```bash
# Start web server
go run . start --database-url "$DATABASE_URL" --port 8080

# Migrations
go run . migrate up --database-url "$DATABASE_URL"
go run . migrate down --database-url "$DATABASE_URL"
go run . migrate status --database-url "$DATABASE_URL"
go run . migrate version --database-url "$DATABASE_URL"
go run . migrate create add_new_table
```

## Environment variables

- `DATABASE_URL` (required): PostgreSQL connection string used by `start` and `migrate`
- `WEBAUTHN_RP_ID` (required): relying party ID for passkeys
- `WEBAUTHN_RP_ORIGINS` (required): comma-separated allowed origins
- `WEBAUTHN_RP_NAME` (optional): display name, defaults to `Fleeti`
- `CSRF_SECRET` (required): secret used to sign CSRF tokens for POST requests
- `BOOTSTRAP_TOKEN` (required for initial setup): token used in `/setup?token=...`

## Key endpoints

- `GET /connectivity`: lightweight connectivity check
- `GET /healthz`: health endpoint
- `GET /login`: login page
- `GET /setup`: first user setup / invite setup flow
- `POST /webauthn/login/start` and `POST /webauthn/login/finish`: passkey login
- `POST /webauthn/setup/start` and `POST /webauthn/setup/finish`: bootstrap/invite setup
- `/update/*`: served update artifacts directory
