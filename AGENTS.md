# AGENTS Guide for Fleeti
This guide is for coding agents working in this repository.
Prefer precise, minimal changes that match existing patterns.

## Scope and Current State
- Main application code is in `src/` (Go web app + DB + templates).
- Nix flake configuration is at repo root and in `nixos/`.
- Vendor dependencies are committed under `src/vendor/`.
- Update artifacts appear in `updates/` (generated, usually not source edits).

## Cursor/Copilot Rules
I checked for agent-specific rule files and found none:
- `.cursorrules`: not present
- `.cursor/rules/`: not present
- `.github/copilot-instructions.md`: not present
If any of these files are added later, treat them as higher-priority instructions.

## Repository Layout
- `src/main.go`: CLI entrypoint (`start`, `migrate`)
- `src/cmd/`: command wiring and startup/migration logic
- `src/db/`: DB pool, schema migration, models, persistence helpers
- `src/routes/`: HTTP handlers, middleware, build pipeline, logging endpoints
- `src/templates/`: server-rendered HTML templates (embedded)
- `src/static/`: CSS/static assets (embedded)
- `nix/`: devshell, formatting, checks, pre-commit hook config
- `nixos/`: NixOS image/update package definitions

## Working Directories
- Run most Go commands from `src/` because `go.mod` is at `src/go.mod`.
- Run flake-wide Nix commands from repository root.

## Build Commands
### Go build/run (from `src/`)
```bash
go build ./...
go run . start --database-url "$DATABASE_URL" --port 8080
```
### DB migrations (from `src/`)
```bash
go run . migrate up --database-url "$DATABASE_URL"
go run . migrate down --database-url "$DATABASE_URL"
go run . migrate status --database-url "$DATABASE_URL"
go run . migrate version --database-url "$DATABASE_URL"
go run . migrate create add_new_table
```
### Nix builds (from repo root)
```bash
nix build .#fleeti
nix build .#fleeti-image
nix build .#fleeti-update
nix run .#run-image
```
- `fleeti` builds the Go package from `src/default.nix`.
- `fleeti-image` builds the full NixOS disk image.
- `fleeti-update` builds OTA update artifacts.

## Lint and Format Commands
### Preferred (Nix-managed toolchain)
```bash
nix develop
nix fmt
nix flake check
```
### Inside devshell (or if tools are available)
```bash
treefmt
gofmt -w .
go vet ./...
golangci-lint run ./...
go-checksec
```
- `nix/treefmt.nix` enables `gofmt`, `nixfmt`, `deadnix`, `statix`, `shellcheck`.
- `nix/checks.nix` configures hooks for `gofmt`, `govet`, `golangci-lint`, `gotest`, `treefmt`.
- `nix/devshells.nix` exposes helper commands `go-checksec` and `go-update`.

## Test Commands (Especially Single Test)
Run from `src/`:
```bash
go test ./...
go test ./routes
go test ./routes -run '^TestBuildLogLive$' -count=1 -v
go test ./routes -run '^TestBuildLogLive$/invalid-after-id$' -count=1 -v
go test -race ./...
```
Current status: there are no `*_test.go` files yet, so these commands report `[no test files]`.

## Code Style Guidelines
### Licensing and file headers
- Keep existing copyright + SPDX header style in Go/Nix/CSS files.
- Preserve package doc comments in `doc.go` when adding packages.

### Formatting and structure
- Always run `gofmt` on changed Go files.
- Prefer small, focused functions and early returns.
- Keep related helper functions in the same file when locality helps readability.
- Use blank lines to separate validation, DB calls, and response rendering phases.

### Imports
- Group imports in this order: standard library, third-party, internal modules.
- Use import aliases only when needed (example: `flamegoTemplate`).
- Avoid unused imports; keep import blocks gofmt-clean.

### Types and data modeling
- Prefer concrete structs for DB records and handler inputs (`Create*Input` pattern).
- Use `any` only for truly dynamic JSON decode paths.
- Return zero-value structs/slices with error on failure, matching current style.
- Keep status/state values centralized in constants (avoid scattered raw strings).

### Naming conventions
- Exported identifiers: `PascalCase`; unexported: `camelCase`.
- Errors: `ErrXxx` vars in `errors.go` with lowercase message strings.
- Keep file names snake_case for feature-focused files (`build_pipeline.go`).
- Route handlers use action-oriented names (`CreateFleet`, `BuildLogLive`, etc.).

### Error handling
- Validate and normalize input early (`strings.TrimSpace`, defaults, bounds checks).
- Wrap propagated errors with context via `%w`.
- Compare sentinel errors with `errors.Is`; inspect typed DB errors with `errors.As`.
- For deferred cleanup, log cleanup failures but keep primary error semantics.
- Avoid panics in request paths; recover in background goroutines and mark failures.

### Logging
- Use `github.com/humaidq/fleeti/v2/logging` package loggers, not ad-hoc globals.
- Prefer structured log fields (`"error"`, `"build_id"`, `"path"`, etc.).
- Keep user-facing flash/error messages concise; log full technical context separately.

### HTTP/routes/templates
- Keep server-rendered HTML via Flamego templates; no separate frontend stack.
- Use PRG flow for forms (`ParseForm` -> mutate -> redirect with flash).
- Set page metadata flags consistently (`setPage`, `IsProfiles`, etc.) for nav state.
- Keep machine endpoints minimal (`/connectivity`, `/healthz`, live log JSON endpoint).

### Database and SQL
- Access DB through shared pool (`db.GetPool`) and guard nil pool with sentinel errors.
- Parameterize SQL (`$1`, `$2`, ...) and avoid string-concatenated user input.
- Always check `rows.Err()` after row iteration.
- Preserve UTC timestamp formatting conventions used in existing queries.
- Keep migrations in `src/db/migrations/` using goose naming (`000NN_name.sql`).

### Nix and shell
- Format Nix with `nixfmt` (via `nix fmt` / `treefmt`).
- Follow existing attribute layout and `mkDefault` usage for overridable values.
- Keep shell snippets compatible with `shellcheck` expectations.

## Things to Avoid
- Do not edit `src/vendor/` manually unless intentionally vendoring dependencies.
- Do not treat `result/`, `nixos/result/`, or `updates/` as source of truth.
- Do not introduce new tooling configs unless needed; prefer existing Nix + Go setup.

## Practical Agent Checklist Before Finishing
1. Run formatters (`nix fmt` or `gofmt` + `treefmt`).
2. Run at least `go test ./...` from `src/`.
3. Run lint checks (`go vet`, `golangci-lint`) when changes are non-trivial.
4. If Nix files changed, run `nix flake check`.
5. Ensure no accidental edits in `src/vendor/` or generated artifact directories.
