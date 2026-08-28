# Repository Guidelines

`krew-toolie` is a Go Discord bot for managing a community tool-sharing library. Members add, borrow, and return tools via slash commands, backed by PostgreSQL through GORM and the [discordgo](https://github.com/bwmarrin/discordgo) library.

## Project Structure & Module Organization

```
cmd/bot/                 # Entry point (main.go: loads .env, starts the bot)
internal/
  bot/                   # Bot wiring, slash command registration
  config/                # Loads DISCORD_TOKEN and DATABASE_URL from env
  database/              # GORM models, DB connection, AutoMigrate
    repository/          # Data-access layer (ToolRepository, UserRepository)
  fuzzy/                 # Token-substring, case-insensitive name matching
  handlers/              # Slash command + button (component) handlers
Dockerfile               # Multi-stage image build for the Discord bot
docker-compose.yml       # Local PostgreSQL service
.env                     # Local secrets (never commit real values)
```

Keep package-level concerns under `internal/`; expose only the executable under `cmd/`.

The database schema is managed via GORM `AutoMigrate` at startup (see `internal/database/db.go`); there is no separate `migrations/` folder.

## Build, Test, and Development Commands

```bash
docker compose up -d        # Start local PostgreSQL
go mod tidy                 # Sync the dependency graph
go build ./...              # Compile all packages
go run ./cmd/bot            # Run the bot locally
go test ./...               # Run all tests
go vet ./...                # Static analysis
docker build -t krew-toolie .                       # Build the bot image
docker build --platform linux/amd64 -t krew-toolie . # Build for amd64 (e.g. NAS)
```

Populate `.env` with `DISCORD_TOKEN` and `DATABASE_URL` before running; PostgreSQL must be up via `docker compose`.

> **Dockerfile check:** whenever a command, dependency, runtime behavior, or the
> target platform changes, verify the `Dockerfile` still builds the image
> successfully (and, if deploying to the NAS, for `linux/amd64`) so the image
> can be loaded/run without errors.

## Commands & Behavior

- `/addtool name [store_link]` — add a tool to your collection. A user cannot have two tools with the same exact name (enforced by a DB unique index on `owner_id + name`).
- `/borrow user_name [tool_name]` — borrow a tool from a user. Fuzzy + case-insensitive lookups for both `user_name` and `tool_name`. With only `user_name`, lists that user's tools. If several tools match, the bot shows interactive buttons to choose one.
- `/return tool_name` — return a currently borrowed tool.
- `/removetool tool_name` — remove one of your own tools (owner-scoped; fuzzy match, with buttons when several match).
- `/mytools` / `/available` — list your tools / all available tools.

Name matching is token-substring: every word of the query must appear somewhere in the target, in any order, ignoring case. E.g. `snow blower` matches `Ego Snow Blower`. See `internal/fuzzy`.

Borrow/return operations lock the affected row (`FOR UPDATE`) inside a transaction to prevent concurrent double-borrowing.

Component buttons carry custom IDs like `borrow:<tool-id>` / `remove:<tool-id>`, handled by `HandleComponentInteraction`.

Slash commands are registered at **guild scope** (instant propagation) when the
bot is in a single server, or to a specific server via the optional `GUILD_ID`
env var. Without a guild target the bot registers commands globally, which
Discord can cache for up to ~1 hour before new/updated commands appear.

## Docker & Deployment

The `Dockerfile` is a multi-stage build that produces a small static binary
(`CGO_ENABLED=0`) and runs it on `alpine` as a non-root user. The bot makes
outbound HTTPS/WebSocket connections to Discord, so no ports are exposed.

`docker-compose.yml` builds the bot image and runs it alongside a Postgres
container. This is the recommended way to run on a NAS (e.g. DXP4800pro) via
SSH/terminal, since it builds natively on the device's own architecture and
avoids cross-architecture image export/import:

```bash
# From the repo root on the NAS (native amd64), build & start all services:
docker compose up -d --build
docker compose logs -f app       # follow app logs

# Rebuild and restart only the app after code changes:
docker compose up -d --build app

# Stop everything (the Postgres data volume is preserved):
docker compose down
```

> Do **not** use `docker compose down -v` unless you want to wipe the Postgres
> volume. Use a plain `docker compose down` to preserve data.

As an alternative, a single bot image (without Postgres) can be built, saved,
and loaded on the NAS:

```bash
docker build -t krew-toolie:latest .
docker save -o krew-toolie.tar krew-toolie:latest
# on the NAS:
docker load -i krew-toolie.tar
```

> **Cross-architecture note:** building `linux/amd64` from an Apple Silicon Mac
> requires `--platform linux/amd64` and runs under QEMU, which can crash the Go
> toolchain (`fatal error: ... free object`). Prefer building on a native amd64
> host (the NAS itself, any Linux x86_64 box, or a GitHub Actions
> `ubuntu-latest` runner).

The image does **not** bundle `.env` (secrets are gitignored); supply
`DISCORD_TOKEN` and `DATABASE_URL` via environment variables or the `env_file`
in `docker-compose.yml`. If the schema or database driver changes, confirm
`AutoMigrate` and the image's runtime still connect to Postgres.

> **Always re-verify the image build** after changing commands, adding
> dependencies, or altering runtime/env behavior — a `go.sum` checksum drift or
> a new cgo dependency can break the build until `Dockerfile`/`go.mod` are
> updated. See the note in Build, Test, and Development Commands.

## Coding Style & Naming Conventions

- Format Go with `gofmt` (and `goimports` if available); use tabs for indentation.
- Exported identifiers use UpperCamelCase, unexported use camelCase.
- Use descriptive domain names (e.g., `ToolRepository`, `handleAddTool`).
- Run `go vet ./...`; no additional linters configured.
- Slash command and option names are lowercase (e.g., `addtool`, `store_link`).

## Testing Guidelines

Tests use GORM with an in-memory SQLite database so they do not require a running Postgres. Because the production models rely on Postgres-specific UUID defaults, the test schema is created with raw DDL in `internal/database/repository/repository_test.go`.

- Place tests beside the code they cover (e.g., `internal/database/repository/tool_repo_test.go`).
- Name functions `Test<Subject>` (e.g., `TestToolBorrow_Success`).
- Run with `go test ./...`; prefer table-driven tests for data-access logic.
- Cover success and error paths (e.g., borrowing an unavailable tool).

## Commit & Pull Request Guidelines

- Use concise, imperative commit messages (e.g., `Add availability query to tool repository`).
- Keep each commit focused on a single logical change.
- PRs should link the related issue, describe the approach, and note manual testing.
- Include screenshots for UI/command behavior changes.
- Ensure `go build ./...` and `go test ./...` pass before requesting review.

## Security & Configuration Tips

- Never commit real tokens or secrets; `.env` is for local use only.
- `DISCORD_TOKEN` is required at runtime, and `DATABASE_URL` defaults to the local Postgres DSN.
- Follow the principle of least privilege when granting bot permissions.
