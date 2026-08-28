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
  handlers/              # Slash command implementations
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
```

Populate `.env` with `DISCORD_TOKEN` and `DATABASE_URL` before running; PostgreSQL must be up via `docker compose`.

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
