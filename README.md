# auto-email

A web-based batch email sending tool powered by the Google Workspace CLI (`gws`). Reads a CSV task list and template files, resolves templates per row, validates everything upfront, then serves a web UI for reviewing and sending each email one-by-one.

## Features

- Template-based email composition with `{{variable}}` syntax resolved from CSV columns
- Full validation before sending: email addresses, attachment paths
- Web UI with real-time progress tracking via SSE
- Attachment preview for PDF, TXT, DOCX, and image files with multi-attachment navigation
- HTML body auto-detection
- Status tracking via `_status` column in the CSV — safe to interrupt and resume
- Google account authentication check at startup with guided setup

## Requirements

- [Google Workspace CLI (`gws`)](https://github.com/googleworkspace/cli/releases) — installed and authenticated
- `pdftotext` (from [poppler-utils](https://poppler.freedesktop.org/)) for PDF attachment preview (optional)

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
| `tasks.csv` | CSV with header row containing recipient data |
| `email_recipient_template.txt` | Template for the recipient email address |
| `email_subject_template.txt` | Template for the email subject |
| `email_body_template.txt` | Template for the email body |
| `email_attachment_template.txt` | Template for attachment file path(s) (optional) |

Then run:

```bash
cd /path/to/working-directory
auto-email
```

Or specify a custom CSV file:

```bash
auto-email --csv my-recipients.csv
```

The tool checks that `gws` is installed and authenticated before launching. If not, it prints setup instructions and exits.

### Templates

Templates use `{{column_name}}` syntax. Whitespace inside brackets is ignored, so `{{col}}` and `{{ col }}` are equivalent.

Example `tasks.csv`:

```csv
name,email,company,invoice
Alice,alice@example.com,Acme Inc,INV-001
Bob,bob@example.com,Globex,INV-002
```

Example `email_recipient_template.txt`:

```
{{ email }}
```

Example `email_attachment_template.txt` (supports comma-separated multiple paths):

```
invoices/{{ invoice }}.pdf, contracts/{{ company }}.docx
```

### Status Tracking

The CSV's `_status` column tracks which emails have been sent. If the column doesn't exist, it's added automatically with all rows set to `Pending`. On successful send, the row is updated to `Sent` and the CSV is saved immediately. Only `Pending` rows are processed, so you can safely interrupt and re-run.

### Web UI

The tool starts a local web server at `http://127.0.0.1:8123` and opens your browser. The UI shows:

- Progress bar and counters
- Current recipient details (to, subject, body preview)
- Attachment preview with `< | >` navigation for multiple attachments
- Send / Skip / Help buttons
- Logged-in Google account

## Testing

```bash
go test ./... -count=1    # all tests
```

## License

MIT
