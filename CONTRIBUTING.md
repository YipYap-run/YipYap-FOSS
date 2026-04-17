# Contributing

Thanks for your interest in YipYap. Here's how to get started.

## Setup

```bash
git clone https://github.com/YipYap-run/YipYap-FOSS.git
cd YipYap-FOSS

# Backend
go mod download

# Frontend
cd web && npm install && cd ..

# Run everything
make dev            # Go backend on :8080
cd web && npm run dev  # Vite dev server on :5173 (proxies API to :8080)
```

Prerequisites: Go 1.26+, Node LTS, Make.

## Making Changes

1. Fork the repo and create a branch: `git checkout -b feat/my-feature`
2. Write tests first, then code. Run `make test` often.
3. Run `make lint` before committing. CI *will* reject lint failures.
4. Keep commits focused. One logical change per commit.
5. Open a pull request against `main`.

## Project Structure

- **Backend:** `internal/` is all Go packages. Each package has a clear responsibility.
- **Frontend:** `web/src/` is a Preact SPA. Pages in `pages/`, shared components in `components/`.
- **Tests:** Co-located with code (`*_test.go`). Integration tests in `internal/integration/`.

## Code Style

**Go:**
- Follow existing patterns. If you're adding a new handler, look at an existing one.
- Interfaces live in `internal/store/store.go` and `internal/notify/provider.go`. Implementations live in their own packages.
- Errors are wrapped with context: `fmt.Errorf("create user: %w", err)`.
- No globals. Dependencies are injected via constructors.

**Frontend (Preact):**
- `class` not `className` (it's Preact, not React).
- `onInput` for text inputs, `onChange` for selects/checkboxes.
- State via `useState` hooks. Global state via `@preact/signals` in `state/`.
- CSS in `web/src/styles/main.css` using CSS custom properties. No CSS-in-JS.

## Testing

```bash
make test          # All Go tests with race detector
make lint          # go vet + golangci-lint

# Run specific package tests
go test ./internal/escalation/... -v
go test ./internal/notify/... -v
go test ./internal/store/sqlite/... -v
```

## Adding a Notification Provider

1. Create `internal/notify/providers/yourprovider.go`
2. Implement the `Notifier` interface: `Channel()`, `Send()`, `MaxConcurrency()`
3. Add a `YourProviderConfig` struct with JSON tags matching the field names
4. Follow the existing pattern: `fallback` config + `decrypt` func + `resolveConfig()` in `Send()`
5. Register it in `cmd/yipyap/notify.go` (the `setupDispatcher` function)
6. Add the secret fields to `secretFields` in `internal/api/handlers/notification_channels.go`
7. Add the type to `CHANNEL_TYPES` in `web/src/pages/settings/index.jsx`
8. Write tests

## Adding a Database Migration

Migrations live in `internal/store/sqlite/migrations/`. Add a new file with the next sequence number:

```
006_my_change.sql
```

Migrations run automatically on startup. Keep them idempotent where possible.

## Questions?

Open an issue at [github.com/YipYap-run/YipYap-FOSS/issues](https://github.com/YipYap-run/YipYap-FOSS/issues).
