# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o auto-email .
./auto-email              # run from a directory containing the template files and CSV
go test ./... -count=1    # run all tests
go test -run TestFull     # run only full flow TUI tests
```

After modifying any code, always run `go test ./... -count=1` to verify nothing is broken.

## Architecture

Single `main` package, three files:

- **main.go** — Entry point, file I/O, CSV parsing with `_status` column management, validation (email addresses via `net/mail`, attachment existence, command PATH lookup, unresolved template vars). Exits with collected errors before TUI starts.
- **template.go** — Regex-based `{{var}}` resolution (`\{\{\s*(\w+)\s*\}\}`). `buildRecipients()` resolves all templates per CSV row: address/subject/body/attachment first from CSV columns, then internal vars (`_address`, `_subject`, `_body`, `_attachment`) override CSV columns for the command template.
- **tui.go** — Bubbletea v2 state machine (`statePreview` → `stateSending` → `stateSent`/`stateError`). Lipgloss v2 for styled boxes. Command execution via `tea.Cmd` closure with `exec.Command`. CSV is rewritten after each successful send.

## Dependencies

- `charm.land/bubbletea/v2` — TUI event loop (note: module path is `charm.land`, not `github.com/charmbracelet`)
- `charm.land/lipgloss/v2` — Terminal styling

## Key Design Decisions

- Bubbletea v2 `View()` returns `tea.View` (not string). Alt screen is set via `v.AltScreen = true` on the View struct, not as a program option.
- CSV is the persistence layer. `_status` column is added automatically if missing. Entire CSV is rewritten on each status update.
- Internal template vars (`_address`, `_subject`, `_body`, `_attachment`) always take precedence over CSV columns with the same names.
- Command strings are split with `strings.Fields()` — no shell quoting support.
