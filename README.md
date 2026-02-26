# Fleeti

Fleeti is a web platform for defining, building, deploying, and monitoring secure Linux system images at fleet scale.

The goal is to let IT teams manage system configuration declaratively through a web UI, build reproducible images with Nix, and deliver signed over-the-air updates to devices using a robust chain of trust.

**Disclaimer:** This README file and other documents in this repository are written with the assistance of AI.

## Vision

- Define full system state from a web dashboard (users, software, policies, behavior).
- Build Linux images from that configuration using Nix.
- Publish update artifacts to controlled server endpoints.
- Roll out updates on devices with `systemd-sysupdate` using an A/B partition scheme.
- Verify authenticity and integrity before and after update (signatures + dm-verity).
- Manage per-device Secure Boot identity and signing keys.
- Monitor deployment health, status, and attestation signals in a unified dashboard.

## Core Capabilities

1. Configuration UI
   - Web-based editor for fleet profiles and system templates.
   - Predefined user sets, package sets, services, and security policies.

2. Image Build Pipeline (Nix)
   - Deterministic image generation from declared configuration.
   - Versioned build outputs and metadata.

3. Signed Update Delivery
   - Server hosts update artifacts at configured endpoints.
   - Device-side update client downloads and verifies updates.

4. Device Update Engine
   - A/B partitioning for safer rollback behavior.
   - `systemd-sysupdate` orchestration and staged rollout support.

5. Secure Boot + Key Management
   - Unique Secure Boot key material per system.
   - Provisioning flow for first boot enrollment.
   - PKI-backed lifecycle management (issuance, rotation, revocation).

6. Integrity and Trust Chain
   - UEFI -> bootloader -> kernel/initrd -> dm-verity protected root.
   - Signed images and verified update metadata.

7. Fleet Observability Dashboard
   - Per-device status, rollout progress, failures, and policy drift.
   - Attestation and trust-state reporting where hardware permits.

## High-Level Architecture

- Control Plane
  - Web UI + API for profile management, rollout policies, and inventory.
- Build/Sign Pipeline
  - Nix-based image builds, artifact manifest generation, and signing.
- Update Distribution Service
  - Serves artifacts and metadata for `systemd-sysupdate` consumption.
- Device Agent
  - Handles enrollment, update checks, verification, and health reporting.
- Trust Services
  - PKI, key storage/HSM integration, cert issuance and revocation.
- Monitoring and Reporting
  - Telemetry ingestion, device state snapshots, and alerting.

## Secure Boot and Provisioning (Initial Notes)

Questions this project is intended to solve:

- How is unique key material provisioned on first boot?
- How are per-device keys/certificates managed server-side?
- How is image signing handled for every release/update?
- How does the client verify signatures and integrity before activation?
- How is post-update integrity enforced (dm-verity) and reported?

Potential direction:

- Use an offline root CA and online intermediate CA(s).
- Enroll devices with hardware-backed identity when available (for example TPM-based identity).
- Issue per-device certificates for authenticated update access and reporting.
- Keep signing keys isolated (ideally HSM-backed) with auditable signing workflows.

## Attestation Scope (x86 Reality)

This platform will include attestation/status reporting, but x86 attestation is more limited and fragmented compared to tightly integrated mobile attestation models.

Initial expectation:

- Support best-effort measured boot and integrity signals where available.
- Expose confidence levels and evidence sources in the dashboard.
- Clearly document platform-specific limitations and trust assumptions.

## Roadmap (Draft)

1. Define core domain model (fleet, profile, build, release, device, rollout).
2. Ship basic profile editor and Nix image build integration.
3. Add signed artifact publishing and `systemd-sysupdate` client flow.
4. Implement A/B update lifecycle with rollback and health checks.
5. Add Secure Boot provisioning and PKI workflows.
6. Add fleet status dashboard and attestation evidence reporting.

## Project Status

This repository currently contains the initial project concept and architecture notes. Implementation details and concrete interfaces will be added incrementally.
