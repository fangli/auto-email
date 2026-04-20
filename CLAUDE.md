# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o auto-email .
./auto-email              # reads tasks.csv from current directory
./auto-email --csv other.csv  # use a different CSV file
go test ./... -count=1    # run all tests
```

After modifying any code, always run `go test ./... -count=1` to verify nothing is broken.

## Architecture

Single `main` package. Web-based UI served from an embedded HTML file.

- **main.go** — Entry point, file I/O, CSV parsing with `_status` column management, `gws` CLI validation and auth check at startup, validation (comma-separated email addresses via `net/mail`, attachment existence). Accepts `--csv` flag for custom CSV path (default: `tasks.csv`). Exits with collected errors before server starts.
- **template.go** — Regex-based `{{var}}` resolution (`\{\{\s*(\w+)\s*\}\}`). `buildRecipients()` resolves all templates per CSV row: recipient/subject/body/attachment from CSV columns. Attachments support comma-separated multiple paths via `splitAttachments()`.
- **server.go** — HTTP server with SSE for real-time UI updates. Hardcoded `gws gmail +send` command with auto-detected `--html` flag and `-a` per attachment. Attachment/preview endpoints support `?index=N` for multi-attachment navigation.
- **ui/index.html** — Embedded single-page web UI with dark theme. Multi-attachment preview with `< N of M >` navigation. Shows logged-in Google account. Help modal with template documentation.

## Dependencies

- Go stdlib: `net/http`, `encoding/csv`, `encoding/json`, `os/exec`, `regexp`, `net/mail`
- Frontend CDN: [mammoth.js](https://cdn.jsdelivr.net/npm/mammoth@1.8.0/mammoth.browser.min.js) for DOCX preview rendering

## Key Design Decisions

- Email sending is hardcoded to `gws gmail +send`. No user-configurable command template.
- HTML body is auto-detected and sent with `--html` flag.
- `gws` installation and authentication are validated at startup with clear guidance if missing.
- CSV is the persistence layer. `_status` column is added automatically if missing. Entire CSV is rewritten on each status update via atomic temp-file rename.
- Attachments are optional and support multiple files (comma-separated in template). Each gets `-a path` in the gws command.
- PDF preview shells out to `pdftotext` (poppler-utils). Falls back gracefully if not installed.
- DOCX preview uses mammoth.js client-side (fetches raw file via `/api/attachment`, renders to HTML in browser).
- `sendCmdFunc` field on `serverState` allows test injection of mock send commands.
