---
title: Overview
description: What Fleeti is and what it currently provides.
---

Fleeti is a web platform for defining, building, deploying, and monitoring Linux
system images at fleet scale.

Today, the project centers on a Go web control plane backed by PostgreSQL,
integrated with Nix-based image builds and update artifact publishing.

## Core concepts

- **Fleet:** A logical group of devices.
- **Profile:** Configuration source that can target one or more fleets.
- **Build:** Versioned image build from a profile revision.
- **Release:** A published build version for deployment.
- **Device:** A registered machine with state and release tracking.
- **Rollout:** Strategy and status for promoting a release to a fleet.

## Current capabilities

- Web UI for fleets, profiles, builds, releases, devices, and rollouts.
- Database migrations and schema management through CLI commands.
- Passkey-based authentication (WebAuthn) for setup and login.
- Build pipeline that runs Nix builds and publishes update artifacts.
- Runtime endpoints for connectivity, health checks, and update file hosting.

## Project status

Fleeti is actively evolving. This wiki starts with an overview and will expand
incrementally with deeper architecture, security, and operations documentation.
