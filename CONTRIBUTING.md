# Contributing to gdhcp

Thank you for your interest in contributing. This guide describes the development workflow, build process, commit expectations, and documentation hierarchy.

## Build and Test

### Prerequisites

- Go 1.26 or later
- Node.js 20 or later
- PostgreSQL 14+ (for integration tests)
- Make

### Build from source

```bash
# Full release build (web + Go binary)
make build                # -> dist/gdhcp

# Go binary only (embeds whatever is in web/dist/)
make go-build             # -> dist/gdhcp

# Angular frontend only
make web                  # -> web/dist/

# Run all tests
make test                 # go test ./...

# Run go vet
make vet                  # go vet ./...

# Run server in dev mode (uses config.yaml or config.yaml.example)
make run-dev              # go run ./server/cmd/gdhcp serve --config ...
```

### Development server

For local development without a full release build:

```bash
cp config.yaml.example config.yaml
# Edit config.yaml if needed (usually defaults work for local dev)

make run-dev
```

The server listens on `http://localhost:8080` by default. The embedded Angular UI is available at that address.

### Running tests

Integration tests require PostgreSQL. The easiest way is to run it in Docker:

```bash
docker run --rm -d \
  --name gdhcp-test-postgres \
  -e POSTGRES_DB=gdhcp \
  -e POSTGRES_USER=gdhcp \
  -e POSTGRES_PASSWORD=gdhcp \
  -p 5432:5432 \
  postgres:16-alpine

# Set the DSN environment variable for tests
export GDHCP_DATABASE_DSN="postgres://gdhcp:gdhcp@localhost:5432/gdhcp?sslmode=disable"

make test

# Cleanup
docker stop gdhcp-test-postgres
```

Alternatively, use the same PostgreSQL service configuration from `.github/workflows/ci.yaml` to run locally.

## Angular frontend development

The web UI is Angular 18 with standalone components and signals. To develop the frontend:

```bash
cd web
npm install
npm start                 # dev server on http://localhost:4200

# In another terminal, run the Go server with CORS:
# Edit config.yaml: cors_allowed_origins: ["http://localhost:4200"]
# Then: make run-dev
```

The dev server at port 4200 proxies API calls to the backend running on port 8080.

## Branch model

- **main**: protected branch; the released line (v0.1.x, v0.2.x, ...). Pre-1.0, so the API and DB schema can still change between minor versions
- **dev**: development branch (default branch); where features land before release
- **feature/*, bugfix/*, etc.**: topic branches off `dev`; deleted after PR merge

All changes land via pull request to `dev`, which is then promoted to `main` at release time.

## Commit messages

Commit messages follow a simple, descriptive style. Read recent commits with:

```bash
git log --oneline main..dev
```

Good commit messages:

- First line is a short summary (50 chars or less)
- Explains the **why**, not just the **what**
- No AI-attribution trailers (no "Co-Authored-By: Claude", no "Generated with", etc.)
- No emojis
- Example: `Bind client identifiers canonically to fix reservation matching` (not `Fix bug`)

Avoid commits that are purely formatting, renaming, or refactoring without a reason. Squash fixup commits before merging.

## Documentation hierarchy

**docs/AMENDMENTS.md is the binding authority.** It overrides ARCHITECTURE.md, CONTRACTS.md, FILETREE.md, and any other doc.

- When the code behavior contradicts a doc, AMENDMENTS.md is always right
- When you make a change that affects external behavior or design, update AMENDMENTS.md, not ARCHITECTURE.md
- AMENDMENTS.md is read-only for production; amendments are added by the project maintainers at release time

Key docs:

- **README.md**: quickstart, deployment paths, CLI tour, first login
- **ARCHITECTURE.md**: design overview (superseded by AMENDMENTS.md where they conflict)
- **AMENDMENTS.md**: binding overrides to architecture and contracts
- **docs/api/endpoints.md**: REST API route reference
- **docs/db/schema.sql**: PostgreSQL schema with inline comments
- **docs/design/*.md**: deep-dive design docs (DHCPv4 engine, data model, HA, frontend, etc.)
- **docs/deploy/*.md**: deployment guides (Docker, bare-metal, Kubernetes, troubleshooting)
- **docs/operations/*.md**: operational runbooks (monitoring, backup/recovery, performance tuning)

## Code style

Match the surrounding code:

- **Go**: standard library style, minimal external dependencies (see AMENDMENTS.md A8 for the approved library list), comments explain **why** not what, no over-abstraction
- **TypeScript/Angular**: Angular 18 conventions, standalone components, signals for state, snake_case in models to match API
- **SQL**: clear column names, inline comments for non-obvious logic, index optimization for production queries
- **YAML**: consistent indentation, human-readable config examples

### No AI-assistant tooling

Never commit these files:

- `.claude/`, `CLAUDE.md` (Claude Code instructions)
- `.cursor/`, `.cursorrules` (Cursor instructions)
- `.gemini/`, `GEMINI.md` (Gemini Code instructions)
- `.codebuddy/`, `CODEBUDDY.md`
- `.qoder/`, `QODER.md`
- `.kiro/`
- `.mcp.json`, `.mcp.local.json`
- `AGENTS.md`
- `opencode.jsonc`

These are excluded in `.gitignore` and `root .gitignore`. Do not add them.

## Testing expectations

- All public API endpoints have integration tests
- DHCP engine has unit tests for lease allocation, binding key matching, and option inheritance
- Database migrations are tested
- CLI commands are tested end-to-end

Before submitting a PR:

```bash
make vet
make test
```

Both must pass. If a test is flaky, document it and fix it; do not skip it.

## PR checklist

Before opening a pull request:

- [ ] Branch off `dev`, not `main`
- [ ] Changes are logically grouped (one feature or bugfix per PR)
- [ ] Commit messages are descriptive and follow the style above
- [ ] `make vet` and `make test` pass
- [ ] No AI-assistant config files added
- [ ] If external behavior changed, AMENDMENTS.md is updated
- [ ] If a new API endpoint, docs/api/endpoints.md is updated
- [ ] If a new CLI command, README.md CLI tour is updated

## Reporting issues

Use GitHub Issues to report bugs or request features. Provide:

- Clear, reproducible steps
- Your deployment method (Docker, bare-metal, Kubernetes, source)
- Relevant configuration and logs
- Version output from `gdhcp version` or the health endpoint

## License

By contributing, you agree your work is licensed under the MIT License (see LICENSE).
