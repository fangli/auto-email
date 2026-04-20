# auto-email

An interactive CLI tool that sends emails one-by-one by executing an external command (e.g., `gws gmail +send`). It reads a CSV recipient list and template files, resolves templates per row, validates everything upfront, then walks you through sending each email with a styled TUI.

## Features

- Template-based email composition with `{{variable}}` syntax resolved from CSV columns
- Full validation before sending: email addresses, attachment paths, command availability, unresolved variables
- Styled full-screen TUI with progress tracking
- Scrollable attachment preview for PDF, TXT, and DOCX files
- Status tracking via `_status` column in the CSV — safe to interrupt and resume
- One-at-a-time sending with Enter to confirm, ESC to abort

## Requirements

- Go 1.22+
- `pdftotext` (from [poppler-utils](https://poppler.freedesktop.org/)) for PDF attachment preview (optional — preview is skipped if not installed)

## Install

```bash
go install github.com/fangli/auto-email@latest
```

Or build from source:

```bash
git clone https://github.com/fangli/auto-email.git
cd auto-email
go build -o auto-email .
```

## Usage

Create a working directory with these files:

| File | Purpose |
|------|---------|
| `email_recipients.csv` | CSV with header row containing recipient data |
| `email_address_template.txt` | Template for the recipient email address |
| `email_subject_template.txt` | Template for the email subject |
| `email_body_template.txt` | Template for the email body |
| `email_attachment_template.txt` | Template for the attachment file path |
| `executable_commandline_template.txt` | Template for the email-sending command |

Then run:

```bash
cd /path/to/working-directory
auto-email
```

### Templates

Templates use `{{column_name}}` syntax. Whitespace inside brackets is ignored, so `{{col}}` and `{{ col }}` are equivalent.

The command template has four internal variables resolved from the other templates: `{{_address}}`, `{{_subject}}`, `{{_body}}`, `{{_attachment}}`. All other `{{var}}` references are resolved from CSV columns.

Example `executable_commandline_template.txt`:

```
gws gmail +send --to {{_address}} --subject {{_subject}} --body {{_body}} --attach {{_attachment}} --from {{sender_email}}
```

### Status Tracking

The CSV's `_status` column tracks which emails have been sent. If the column doesn't exist, it's added automatically with all rows set to `Pending`. On successful send, the row is updated to `Sent` and the CSV is saved immediately. Only `Pending` rows are processed, so you can safely interrupt and re-run.

### TUI Controls

| Key | Action |
|-----|--------|
| Enter | Send the current email (or retry on error) |
| ESC | Cancel and exit |
| ↑↓ / PgUp·PgDn | Scroll attachment preview |

## Testing

```bash
go test ./... -count=1    # all tests
go test -run TestFull     # full flow tests only
```

## License

MIT
