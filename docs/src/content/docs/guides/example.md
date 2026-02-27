---
title: Quickstart
description: Run Fleeti locally with PostgreSQL, WebAuthn, and migrations.
---

## Prerequisites

- Go toolchain
- PostgreSQL
- Nix (required for image builds from the web UI)

## 1) Configure environment

Set the required variables:

```bash
export DATABASE_URL='postgres://user:pass@localhost:5432/fleeti?sslmode=disable'
export WEBAUTHN_RP_ID='localhost'
export WEBAUTHN_RP_ORIGINS='http://localhost:8080'
export CSRF_SECRET='replace-with-a-random-secret'
export BOOTSTRAP_TOKEN='replace-with-a-random-secret'
```

Optional:

```bash
export WEBAUTHN_RP_NAME='Fleeti'
```

## 2) Run database migrations

From `src/`:

```bash
go run . migrate up --database-url "$DATABASE_URL"
```

## 3) Start the app

From `src/`:

```bash
go run . start --database-url "$DATABASE_URL" --port 8080
```

## 4) Complete first-time setup

Open:

`http://localhost:8080/setup?token=<BOOTSTRAP_TOKEN>`

Then register the first admin passkey via WebAuthn.
