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

## Commands & Behavior

- `/addtool name [store_link]` — add a tool to your collection. A user cannot have two tools with the same exact name (enforced by a DB unique index on `owner_id + name`).
- `/borrow tool_name [owner]` — borrow a tool by fuzzy name, optionally narrowed to an owner. `owner` matches a user's username, global display name, or server nickname (fuzzy + case-insensitive). A single match shows a **Confirm/Cancel** prompt; multiple matches show a **select menu** (with a Cancel option) to choose which tool/owner to borrow.
- `/return tool_name` — return a tool the caller is currently borrowing (borrower-scoped). Single match → **Confirm/Cancel**; multiple matches → **select menu** with Cancel.
- `/removetool tool_name` — remove one of the caller's own tools (owner-scoped). Single match → **Confirm/Cancel**; multiple matches → **select menu** with Cancel.
- `/mytools` / `/available` — list your tools / all available tools.

Responses are **ephemeral** for informational listings, error messages, confirm prompts, and select menus. Only the **successful** borrow, return, add, and remove actions post a public message to the channel. Tool/owner names in prompts use the owner's server nickname, then global name, then username.

Name matching is token-substring: every word of the query must appear somewhere in the target, in any order, ignoring case. E.g. `snow blower` matches `Ego Snow Blower`. See `internal/fuzzy`.

Borrow/return operations lock the affected row (`FOR UPDATE`) inside a transaction to prevent concurrent double-borrowing.

Component buttons carry custom IDs like `borrow:<tool-id>` / `remove:<tool-id>`, handled by `HandleComponentInteraction`.

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
