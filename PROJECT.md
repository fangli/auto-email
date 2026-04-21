# PROJECT.md

## Requirements

### Overview

A web-based batch email sending tool powered by the Google Workspace CLI (`gws gmail +send`). The tool reads a CSV task list and template files, resolves templates per row, validates everything upfront, then serves a web UI for reviewing and sending each email one-by-one.

### Bootstrap

When the executable starts, it:

1. Checks that `gws` is installed and authenticated (via `gws gmail users getProfile`). If not, prints setup instructions and exits.
2. Reads these files from the current working directory:
   - `tasks.csv` (default, overridable with `--csv`) — CSV with header row containing recipient data.
   - `email_recipient_template.txt` — Template for the recipient email address.
   - `email_subject_template.txt` — Template for the email subject.
   - `email_body_template.txt` — Template for the email body.
   - `email_attachment_template.txt` (optional) — Template for attachment file path(s). Supports comma-separated multiple paths.
   - `.env` (optional) — `KEY=VALUE` environment variables passed to all `gws` commands (auth check and send).

### CSV Value Normalization

All values read from the CSV are trimmed of leading and trailing whitespace. Short rows are padded to match header count.

### Template Parsing

Templates use `{{column_name}}` syntax (double curly brackets). The parser trims whitespace inside brackets, so `{{col}}` and `{{ col }}` resolve identically.

### Validation

At bootstrap, before the web server starts, the app validates all pending entries:

- Email addresses must be valid and non-empty (parsed via `net/mail`). Comma-separated addresses are supported.
- Attachment paths (each one in the comma-separated list) must point to existing files.

On any validation error, the app prints all errors at once with row number, field, value, and a fix suggestion, then exits.

### Email Sending

Emails are sent using a hardcoded `gws gmail +send` command:

```
gws gmail +send --to <address> --subject <subject> --body <body> [--html] [-a file1] [-a file2] ...
```

- HTML body is auto-detected (regex: `<[a-zA-Z][\s\S]*>`) and sent with `--html` flag.
- Each attachment gets a separate `-a` flag.
- Command timeout: 2 minutes. Output capture limit: 64 KB.

### Web UI

Once bootstrap completes, the app starts an HTTP server on an available port and opens the browser. The UI provides:

1. Progress bar and counters (current/pending/sent/total).
2. Recipient details: To, Subject, Body (rendered in iframe).
3. Attachment preview with `< N of M >` navigation for multiple attachments. Supports PDF (inline), images, TXT, and DOCX (text extraction).
4. Send / Skip buttons. Retry on error.
5. Auto-send mode with configurable interval (5/10/30/60/120s). Countdown starts after each successful send. Stops on error or completion.
6. Help modal with template file documentation.
7. Logged-in Google account display.
8. Real-time updates via Server-Sent Events (SSE).

### Status Tracking

- The CSV may or may not have a `_status` column. If missing, the app adds it as the last column with default value `Pending` and saves immediately.
- On successful send, the entry is updated to `Sent` and the CSV is saved via atomic temp-file rename.
- On skip, the entry is updated to `Skipped`.
- Only entries with `_status == "Pending"` are processed.

---

## Design

### File Structure

```
auto-email/
  go.mod
  go.sum
  main.go             — Entry point, bootstrap, gws auth check, validation
  template.go         — Template parsing, resolution, recipient building
  server.go           — HTTP server, SSE, send execution, attachment serving
  ui/index.html       — Embedded web UI (single-page, dark theme)
  template_test.go    — Tests for template resolution and recipient building
  main_test.go        — Tests for file I/O, CSV, validation, bootstrap integration
  server_test.go      — Tests for HTTP endpoints, send flow, SSE
  CLAUDE.md           — Claude Code guidance
  PROJECT.md          — This file
  README.md           — User-facing documentation
```

### Dependencies

Go stdlib: `net/http`, `encoding/csv`, `encoding/json`, `os/exec`, `regexp`, `net/mail`, `os`, `fmt`, `strings`, `time`, `io`, `path/filepath`, `context`, `sync`, `net`

Frontend CDN: [mammoth.js@1.8.0](https://cdn.jsdelivr.net/npm/mammoth@1.8.0/mammoth.browser.min.js) for client-side DOCX rendering

### Key Types

```go
type Recipient struct {
    Row         int
    Address     string
    Subject     string
    Body        string
    Attachments []string  // comma-split from template, each trimmed
    Status      string    // "Pending", "Sent", or "Skipped"
}

type AppData struct {
    Headers    []string
    Rows       [][]string
    StatusCol  int
    Recipients []Recipient
    CSVPath    string
    BaseDir    string
    LoggedInAs string
}
```

### Template Resolution

Regex: `\{\{\s*(\w+)\s*\}\}` — matches `{{var}}` with optional inner whitespace.

Resolution order per CSV row:

1. Build `vars` map from CSV columns.
2. Resolve recipient, subject, body, and attachment templates using `vars`.
3. Split resolved attachment string by comma, trim each, filter empty → `Attachments []string`.

### Server Architecture

- `serverState` holds mutable state under a mutex: cursor position, send state, SSE clients.
- `sendCmdFunc` field allows test injection of mock send commands (defaults to `defaultSendCmd` which runs `gws`).
- `buildStatus()` constructs JSON response with `attachments []attachmentJSON` (path/ext/size per file) and `loggedInAs`.
- Attachment and preview endpoints accept `?index=N` query param for multi-attachment support.
- SSE handler selects on `s.done` channel to exit cleanly when all tasks complete, preventing shutdown timeout.

### Attachment Preview

Supported formats:
- `.pdf` — rendered inline via iframe in web UI (browser-native PDF viewer).
- `.txt` — reads file directly server-side, up to 1 MB. Rendered in `<pre>` tag.
- `.docx` — rendered client-side via mammoth.js (fetches raw file from `/api/attachment`, converts to semantic HTML in browser).
- Images (png/jpg/gif/webp/svg/bmp) — displayed inline via `<img>` tag.

All fetch-based previews (DOCX, TXT) use a unified `fetchAttachment()` JS helper with cache-busting and staleness checks.

### CSV Persistence

`saveCSV()` writes to a temp file then renames atomically. The row is updated in memory first, then written to disk. If the write fails, the server shows an error but the row stays `Pending`, which is safe for re-run.

---

## Testing

### Test Structure

- `template_test.go` — `resolveTemplate`, `hasUnresolved`, `splitAttachments`, `buildRecipients` (basic, skips_sent, missing_var, multiple_attachments, empty_attachment, etc.)
- `main_test.go` — File I/O, CSV parsing, validation (emails, attachments, multi-attachment), status column addition, bootstrap integration
- `server_test.go` — HTTP endpoints (status, send, skip, attachment, preview, SSE, index), state transitions, error/retry flows, full flow simulations

### Test Approach

Server tests use `httptest` with a mock `sendCmdFunc` to avoid requiring `gws` in the test environment. State transitions are verified by polling `/api/status`.

### Running Tests

```bash
go test ./... -count=1    # all tests, no cache
go test ./... -v          # verbose output
```
