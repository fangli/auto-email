package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTemplate(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "t.txt")
		os.WriteFile(p, []byte("  hello  \n"), 0644)
		got, err := readTemplate(p)
		if err != nil {
			t.Fatal(err)
		}
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("crlf", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "t.txt")
		os.WriteFile(p, []byte("a\r\nb\r\n"), 0644)
		got, err := readTemplate(p)
		if err != nil {
			t.Fatal(err)
		}
		if got != "a\nb" {
			t.Errorf("got %q, want %q", got, "a\nb")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		_, err := readTemplate("/nonexistent/path/file.txt")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestReadBodyTemplate(t *testing.T) {
	t.Run("preserves_internal_newlines", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "b.txt")
		os.WriteFile(p, []byte("\n  Hello\n\nWorld\n  "), 0644)
		got, err := readBodyTemplate(p)
		if err != nil {
			t.Fatal(err)
		}
		if got != "Hello\n\nWorld" {
			t.Errorf("got %q, want %q", got, "Hello\n\nWorld")
		}
	})

	t.Run("crlf", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "b.txt")
		os.WriteFile(p, []byte("line1\r\nline2\r\n"), 0644)
		got, err := readBodyTemplate(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "\r") {
			t.Errorf("CRLF not normalized: %q", got)
		}
		if got != "line1\nline2" {
			t.Errorf("got %q, want %q", got, "line1\nline2")
		}
	})
}

func TestLoadCSV(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "test.csv")
		os.WriteFile(p, []byte("name,email\nAlice,a@test.com\nBob,b@test.com\n"), 0644)
		headers, rows, err := loadCSV(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(headers) != 2 || headers[0] != "name" || headers[1] != "email" {
			t.Errorf("headers = %v", headers)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rows))
		}
		if rows[0][0] != "Alice" || rows[0][1] != "a@test.com" {
			t.Errorf("row 0 = %v", rows[0])
		}
	})

	t.Run("trims_cells", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "test.csv")
		os.WriteFile(p, []byte(" name , email \n Alice , a@test.com \n"), 0644)
		headers, rows, err := loadCSV(p)
		if err != nil {
			t.Fatal(err)
		}
		if headers[0] != "name" || headers[1] != "email" {
			t.Errorf("headers not trimmed: %v", headers)
		}
		if rows[0][0] != "Alice" || rows[0][1] != "a@test.com" {
			t.Errorf("cells not trimmed: %v", rows[0])
		}
	})

	t.Run("empty_file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "test.csv")
		os.WriteFile(p, []byte(""), 0644)
		_, _, err := loadCSV(p)
		if err == nil {
			t.Fatal("expected error for empty file")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error should mention empty: %v", err)
		}
	})

	t.Run("headers_only", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "test.csv")
		os.WriteFile(p, []byte("name,email\n"), 0644)
		headers, rows, err := loadCSV(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(headers) != 2 {
			t.Errorf("got %d headers, want 2", len(headers))
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})
}

func TestSaveCSV_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.csv")
	headers := []string{"a", "b", "c"}
	rows := [][]string{{"1", "2", "3"}, {"4", "5", "6"}}

	if err := saveCSV(p, headers, rows); err != nil {
		t.Fatal(err)
	}

	gotH, gotR, err := loadCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotH) != 3 || gotH[0] != "a" || gotH[1] != "b" || gotH[2] != "c" {
		t.Errorf("headers = %v", gotH)
	}
	if len(gotR) != 2 {
		t.Fatalf("got %d rows, want 2", len(gotR))
	}
	for i := range rows {
		for j := range rows[i] {
			if gotR[i][j] != rows[i][j] {
				t.Errorf("row[%d][%d] = %q, want %q", i, j, gotR[i][j], rows[i][j])
			}
		}
	}
}

func TestIndexOf(t *testing.T) {
	headers := []string{"a", "b", "c"}
	if got := indexOf(headers, "b"); got != 1 {
		t.Errorf("indexOf b = %d, want 1", got)
	}
	if got := indexOf(headers, "z"); got != -1 {
		t.Errorf("indexOf z = %d, want -1", got)
	}
	if got := indexOf(headers, "a"); got != 0 {
		t.Errorf("indexOf a = %d, want 0", got)
	}
	if got := indexOf(nil, "x"); got != -1 {
		t.Errorf("indexOf on nil = %d, want -1", got)
	}
}

func TestValidate(t *testing.T) {
	makeAttachment := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "attach.pdf")
		os.WriteFile(p, []byte("data"), 0644)
		return p
	}

	t.Run("valid", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: attach, Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) > 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("invalid_email", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "not-an-email", Attach: attach, Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for invalid email")
		}
		if !strings.Contains(errs[0], "Invalid email") {
			t.Errorf("error should mention invalid email: %s", errs[0])
		}
	})

	t.Run("empty_email", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "", Attach: attach, Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for empty email")
		}
	})

	t.Run("missing_attachment", func(t *testing.T) {
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: "/nonexistent/file.pdf", Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for missing attachment")
		}
		if !strings.Contains(errs[0], "not found") {
			t.Errorf("error should mention not found: %s", errs[0])
		}
	})

	t.Run("directory_attachment", func(t *testing.T) {
		dir := t.TempDir()
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: dir, Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for directory attachment")
		}
		if !strings.Contains(errs[0], "directory") {
			t.Errorf("error should mention directory: %s", errs[0])
		}
	})

	t.Run("missing_command", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: attach, Command: "nonexistent_xyz_cmd arg1", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for missing command")
		}
		if !strings.Contains(errs[0], "not found on PATH") {
			t.Errorf("error should mention not found on PATH: %s", errs[0])
		}
	})

	t.Run("empty_command", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: attach, Command: "", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for empty command")
		}
		if !strings.Contains(errs[0], "empty") {
			t.Errorf("error should mention empty: %s", errs[0])
		}
	})

	t.Run("unresolved_vars", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{{Row: 0, Address: "a@test.com", Attach: attach, Command: "echo {{missing}}", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) == 0 {
			t.Fatal("expected error for unresolved vars")
		}
		if !strings.Contains(errs[0], "Unresolved") {
			t.Errorf("error should mention Unresolved: %s", errs[0])
		}
	})

	t.Run("sent_skipped", func(t *testing.T) {
		recipients := []Recipient{{Row: 0, Address: "bad", Attach: "/missing", Command: "", Status: "Sent"}}
		errs := validate(recipients)
		if len(errs) > 0 {
			t.Errorf("sent rows should be skipped, got errors: %v", errs)
		}
	})

	t.Run("multiple_errors", func(t *testing.T) {
		recipients := []Recipient{{Row: 0, Address: "bad", Attach: "/missing/file", Command: "echo test", Status: "Pending"}}
		errs := validate(recipients)
		if len(errs) < 2 {
			t.Errorf("expected at least 2 errors, got %d: %v", len(errs), errs)
		}
	})

	t.Run("command_lookup_cached", func(t *testing.T) {
		attach := makeAttachment(t)
		recipients := []Recipient{
			{Row: 0, Address: "a@test.com", Attach: attach, Command: "nonexistent_xyz_cmd arg1", Status: "Pending"},
			{Row: 1, Address: "b@test.com", Attach: attach, Command: "nonexistent_xyz_cmd arg2", Status: "Pending"},
		}
		errs := validate(recipients)
		cmdErrs := 0
		for _, e := range errs {
			if strings.Contains(e, "not found on PATH") {
				cmdErrs++
			}
		}
		if cmdErrs != 1 {
			t.Errorf("expected 1 command error (cached), got %d", cmdErrs)
		}
	})
}

func TestStatusColumnAddition(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.csv")

	headers := []string{"name", "email"}
	rows := [][]string{{"Alice", "a@test.com"}, {"Bob", "b@test.com"}}
	if err := saveCSV(p, headers, rows); err != nil {
		t.Fatal(err)
	}

	headers, rows, err := loadCSV(p)
	if err != nil {
		t.Fatal(err)
	}

	statusCol := indexOf(headers, "_status")
	if statusCol != -1 {
		t.Fatal("_status should not exist yet")
	}

	headers = append(headers, "_status")
	statusCol = len(headers) - 1
	for i := range rows {
		rows[i] = append(rows[i], "Pending")
	}

	if err := saveCSV(p, headers, rows); err != nil {
		t.Fatal(err)
	}

	headers2, rows2, err := loadCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if indexOf(headers2, "_status") != statusCol {
		t.Errorf("_status column not found after reload")
	}
	for i, row := range rows2 {
		if row[statusCol] != "Pending" {
			t.Errorf("row %d status = %q, want Pending", i, row[statusCol])
		}
	}
}

func TestBootstrapIntegration(t *testing.T) {
	dir := t.TempDir()

	attachPath := filepath.Join(dir, "invoice.pdf")
	os.WriteFile(attachPath, []byte("pdf content"), 0644)

	os.WriteFile(filepath.Join(dir, "email_recipients.csv"), []byte("name,email,_status\nAlice,alice@test.com,Pending\nBob,bob@test.com,Pending\nCarol,carol@test.com,Sent\n"), 0644)
	os.WriteFile(filepath.Join(dir, "email_address_template.txt"), []byte("{{email}}"), 0644)
	os.WriteFile(filepath.Join(dir, "email_subject_template.txt"), []byte("Invoice for {{name}}"), 0644)
	os.WriteFile(filepath.Join(dir, "email_body_template.txt"), []byte("Dear {{name}},\n\nPlease find attached.\n\nRegards"), 0644)
	os.WriteFile(filepath.Join(dir, "email_attachment_template.txt"), []byte(attachPath), 0644)
	os.WriteFile(filepath.Join(dir, "executable_commandline_template.txt"), []byte("echo {{_address}} {{_subject}}"), 0644)

	csvPath := filepath.Join(dir, "email_recipients.csv")
	addrTmpl, err := readTemplate(filepath.Join(dir, "email_address_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	subjectTmpl, err := readTemplate(filepath.Join(dir, "email_subject_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	bodyTmpl, err := readBodyTemplate(filepath.Join(dir, "email_body_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	attachTmpl, err := readTemplate(filepath.Join(dir, "email_attachment_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cmdTmpl, err := readTemplate(filepath.Join(dir, "executable_commandline_template.txt"))
	if err != nil {
		t.Fatal(err)
	}

	headers, rows, err := loadCSV(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	statusCol := indexOf(headers, "_status")
	if statusCol == -1 {
		t.Fatal("_status column should exist")
	}

	recipients, buildErrs := buildRecipients(headers, rows, statusCol, addrTmpl, subjectTmpl, bodyTmpl, attachTmpl, cmdTmpl)
	if len(buildErrs) > 0 {
		t.Fatalf("build errors: %v", buildErrs)
	}

	valErrs := validate(recipients)
	if len(valErrs) > 0 {
		t.Fatalf("validation errors: %v", valErrs)
	}

	if len(recipients) != 3 {
		t.Fatalf("got %d recipients, want 3", len(recipients))
	}

	pending := 0
	sent := 0
	for _, r := range recipients {
		if r.Status == "Sent" {
			sent++
		} else {
			pending++
		}
	}
	if pending != 2 || sent != 1 {
		t.Errorf("pending=%d sent=%d, want 2 and 1", pending, sent)
	}

	if recipients[0].Address != "alice@test.com" {
		t.Errorf("first address = %q", recipients[0].Address)
	}
	if recipients[0].Subject != "Invoice for Alice" {
		t.Errorf("first subject = %q", recipients[0].Subject)
	}
	if !strings.Contains(recipients[0].Body, "Dear Alice") {
		t.Errorf("first body = %q", recipients[0].Body)
	}
	if recipients[0].Command != "echo alice@test.com Invoice for Alice" {
		t.Errorf("first command = %q", recipients[0].Command)
	}
}
