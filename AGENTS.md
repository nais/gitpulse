# AGENTS.md

## Project
`gitpulse` — Go TUI dashboard monitoring GitHub repos across orgs. Shows repo health, open PRs, recent pushes, local git status, and active AI agent sessions.

## Structure
```
main.go           — Entrypoint, CLI flags, data conversion
config.go         — TOML config loader
github.go         — GitHub GraphQL client
dashboard.go      — Data fetching orchestration
internal/tui/     — Bubbletea TUI (rendering, navigation)
internal/local/   — Local git scanning, agent session detection
internal/cache/   — File-based JSON cache
```

## Commands
```sh
go build -o gitpulse .   # build
go test ./...            # test
go vet ./...             # lint
```

## Auth
Uses `GITHUB_TOKEN` env var, falls back to `gh auth token`.

## Conventions
- Idiomatic Go, early returns, errors wrapped with `%w`
- No global state — pass config/clients via parameters
- `config.toml` (user-specific, gitignored)

