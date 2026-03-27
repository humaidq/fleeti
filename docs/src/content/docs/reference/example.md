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
- `OPENROUTER_API_KEY` (optional): enables the profile AI wizard
- `OPENROUTER_MODEL` (optional): OpenRouter model for the profile AI wizard, defaults to `openai/gpt-5-mini`
- `OPENROUTER_BASE_URL` (optional): override the OpenRouter chat completions endpoint
- `OPENROUTER_HTTP_REFERER` (optional): forwarded to OpenRouter for request attribution

## Key endpoints

- `GET /connectivity`: lightweight connectivity check
- `GET /healthz`: health endpoint
- `GET /api/v1/profiles`: list profiles visible to the authenticated API key owner
- `GET /api/v1/profiles/{id}`: fetch the latest configuration for a specific visible profile
- `GET /api/v1/profiles/{id}/builds`: list builds for a specific visible profile
- `GET /api/v1/profiles/{id}/builds/{buildId}`: fetch a specific build for a visible profile
- `GET /api/v1/profiles/{id}/builds/{buildId}/logs`: poll incremental logs for a queued or running build
- `POST /api/v1/profiles/{id}/builds`: queue a new build for a manageable profile
- `PUT /api/v1/profiles/{id}`: replace the latest stored profile configuration
- `PATCH /api/v1/profiles/{id}`: partially update the latest stored profile configuration
- `GET /profiles/wizard`: AI-assisted draft flow for creating a new profile
- `GET /profiles/{id}/wizard`: AI-assisted draft flow for adapting an existing profile
- `GET /login`: login page
- `GET /setup`: first user setup / invite setup flow
- `POST /webauthn/login/start` and `POST /webauthn/login/finish`: passkey login
- `POST /webauthn/setup/start` and `POST /webauthn/setup/finish`: bootstrap/invite setup
- `/update/*`: served update artifacts directory
