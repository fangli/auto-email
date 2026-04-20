# PROJECT.md

## Requirements

### Overview

An interactive CLI tool that sends emails (Gmail) by executing an external email-sending command (e.g., `gws gmail +send`). The tool reads a CSV recipient list and template files, resolves templates per row, validates everything upfront, then walks the user through sending each email one-by-one with a styled TUI.

### Bootstrap

When the executable starts, it reads these files from the current working directory:

- `email_recipients.csv` — CSV with header row containing recipient data.
- `email_address_template.txt` — Template for the recipient email address.
- `email_attachment_template.txt` — Template for the attachment file path. Only a single attachment is supported.
- `email_body_template.txt` — Template for the email body.
- `email_subject_template.txt` — Template for the email subject.
- `executable_commandline_template.txt` — Template for the email-sending command. Contains four internal template variables prefixed with underscore: `{{_address}}`, `{{_attachment}}`, `{{_body}}`, `{{_subject}}`. These are resolved from the other template files. All other `{{var}}` references are resolved from CSV columns. Example content: `gws gmail +send --to {{_address}} --subject {{_subject}} --body {{_body}} --attach {{_attachment}} --from {{my_own_email_addr}}`.

If a CSV column has the same name as an internal template variable (e.g., `_address`), the internal variable always takes precedence.

### CSV Value Normalization

All values read from the CSV are trimmed of leading and trailing whitespace.

### Template Parsing

Templates use `{{column_name}}` syntax (double curly brackets). The parser trims whitespace inside brackets, so `{{col}}` and `{{ col }}` resolve identically.

### Validation

At bootstrap, before any sending, the app validates all pending entries:

- Email addresses must be valid and non-empty (parsed via `net/mail`).
- Attachment paths must point to existing files.
- The command executable (first whitespace-separated token) must exist on PATH.
- All template variables must be resolved (no remaining `{{...}}`).

On any validation error, the app prints all errors at once with row number, field, value, and a fix suggestion, then exits.

### User Experience

Once bootstrap completes, the app processes entries from top to bottom:

1. For each entry, clear the screen and display parsed email info (recipient, subject, body, attachment path and size) in a styled TUI box.
2. Prompt: press Enter to send, ESC to cancel.
3. On ESC: exit with a summary of total sent (all time), sent this run, and remaining.
4. On Enter: execute the resolved command.
   - Show "Sending..." status in-place (no scrolling).
   - On success: display "Sent Successfully" for 1 second, then auto-advance to the next entry.
   - On error: display the error from the subprocess, stay on the current entry, and prompt Enter to retry or ESC to abort.

This continues until all pending entries are sent.

### Status Tracking

- The CSV may or may not have a `_status` column. If missing, the app adds it as the last column with default value `Pending` and saves immediately.
- On successful send, the entry is updated to `Sent` and the CSV is saved.
- On failed send, the entry stays `Pending`.
- Only entries with `_status == "Pending"` are processed.

---

## Design

### File Structure

```
auto-email/
  go.mod
  go.sum
  main.go             — Entry point, bootstrap, validation
  template.go         — Template parsing and resolution
  tui.go              — Bubbletea model, state machine, lipgloss rendering
  template_test.go    — Tests for template resolution and recipient building
  main_test.go        — Tests for file I/O, CSV, validation, bootstrap integration
  tui_test.go         — TUI state transition, view rendering, full flow simulation
  CLAUDE.md           — Claude Code guidance
  PROJECT.md          — This file
  testdata/           — Sample fixtures for manual testing
```

### Dependencies

- `charm.land/bubbletea/v2` — TUI event loop, keypress handling, alt-screen
- `charm.land/bubbles/v2` — Viewport component for scrollable attachment preview
- `charm.land/lipgloss/v2` — Bordered boxes, colored text
- Stdlib: `encoding/csv`, `os/exec`, `regexp`, `net/mail`, `archive/zip`, `encoding/xml`, `os`, `fmt`, `strings`, `time`, `io`, `path/filepath`

### Key Types

```go
type Recipient struct {
    Row     int       // 0-based index into CSV data rows
    Address string
    Subject string
    Body    string
    Attach  string
    Command string
    Status  string    // "Pending" or "Sent"
}

type AppData struct {
    Headers    []string
    Rows       [][]string   // mutable for status updates
    StatusCol  int
    Recipients []Recipient
    CSVPath    string
}
```

### Template Resolution

Regex: `\{\{\s*(\w+)\s*\}\}` — matches `{{var}}` with optional inner whitespace.

Resolution order per CSV row:

1. Build `vars` map from CSV columns.
2. Resolve address, subject, body, and attachment templates using `vars`.
3. Add internal vars (`_address`, `_subject`, `_body`, `_attachment`) to `vars`, overriding any CSV columns with the same names.
4. Resolve command template using the augmented `vars`.

### TUI State Machine

```
statePreview  ──Enter──▶  stateSending  ──success──▶  stateSent  ──1s tick──▶  statePreview (next)
                                         ──error────▶  stateError ──Enter────▶  stateSending (retry)
              ──ESC────▶  quit                                     ──ESC────▶  quit
```

- Command execution: plain `tea.Cmd` closure running `exec.Command` with `CombinedOutput()`. Bubbletea runs it in a goroutine; the TUI renders "Sending..." while it executes.
- Auto-advance: `tea.Tick(1s)` fires `autoAdvanceMsg` after successful send.
- Alt screen: set via `v.AltScreen = true` on the `tea.View` struct (bubbletea v2 API).
- Out-of-bounds guard: `View()` returns empty content when `cursor >= len(pending)` to prevent panic on final render after quit.

### TUI Layout

The TUI uses a three-section layout pinned to the terminal height:

- **Top (fixed)**: Email info box — To, Subject, Body with separator rows
- **Middle (flexible)**: Attachment box — shows file path and size; for `.pdf`, `.txt`, and `.docx` files, includes a scrollable text preview via the bubbles viewport component
- **Bottom (pinned)**: Status line (fixed 1-line height), progress text, progress bar, prompt box

The `renderLayout()` method handles all states, with `renderPreview()` and `renderError()` as thin wrappers that pass the appropriate prompt text and status message.

### Attachment Preview

Supported formats:
- `.pdf` — shells out to `pdftotext` (poppler-utils); falls back gracefully if not installed
- `.txt` — reads file directly via `os.ReadFile`
- `.docx` — DIY parser using stdlib `archive/zip` + `encoding/xml`, extracts `<w:t>` text from `word/document.xml`

All formats return `""` on any error, which hides the preview and shows only the attachment info line.

### CSV Persistence

`saveCSV()` rewrites the entire CSV after each successful send. The row is updated in memory first (`Rows[row][statusCol] = "Sent"`), then written to disk. If the write fails, the TUI shows an error but doesn't crash — the row stays `Pending`, which is safe for re-run.

### Design Decisions

- **Single `main` package**: no sub-packages needed for ~500 lines of code.
- **CSV as persistence**: no database; the CSV is small and rewriting it is atomic enough for this use case.
- **`strings.Fields()` for command splitting**: handles the common case. Does not support quoted arguments with spaces.
- **Validation collects all errors**: prints them all at once so the user can fix everything in one pass.
- **`seenExec` map in validation**: deduplicates command PATH lookups across rows.

---

## Testing

### Test Structure

- `template_test.go` — 21 subtests covering `resolveTemplate`, `hasUnresolved`, `buildRecipients`
- `main_test.go` — 25 subtests covering file I/O, CSV parsing, validation, status column addition, bootstrap integration
- `tui_test.go` — 31 subtests covering key sanity, state transitions, view rendering, full flow simulations, attachment preview extraction (PDF/TXT/DOCX)

### TUI Test Approach

Tests drive the bubbletea model directly via `Update()` message injection — no TTY required:

```go
// Inject a keypress
result, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

// Simulate send result
result, cmd = m.Update(sendResultMsg{nil})

// Simulate auto-advance timer
result, cmd = m.Update(autoAdvanceMsg{})

// Check for quit
isQuitCmd(cmd)  // executes cmd, checks for tea.QuitMsg
```

### Full Flow Simulations

- **TestFullFlowTwoRecipientsSendAll** — preview → send → sent → advance → preview → send → sent → advance → quit, verifies CSV on disk
- **TestFullFlowErrorThenRetry** — send → error → retry → success → quit
- **TestFullFlowEscFromPreview** — immediate ESC, verifies no CSV changes
- **TestFullFlowEscFromError** — send → error → ESC, verifies row stays Pending

### Running Tests

```bash
go test ./... -count=1    # all tests, no cache
go test -run TestFull     # full flow tests only
go test ./... -v          # verbose output
```
