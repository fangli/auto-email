package main

import (
	"testing"
)

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		name        string
		tmpl        string
		vars        map[string]string
		wantResult  string
		wantMissing []string
	}{
		{"single_var", "Hello {{name}}", map[string]string{"name": "Alice"}, "Hello Alice", nil},
		{"multiple_vars", "{{a}} {{b}}", map[string]string{"a": "X", "b": "Y"}, "X Y", nil},
		{"missing_var", "Hello {{name}}", map[string]string{}, "Hello {{name}}", []string{"name"}},
		{"partial_missing", "{{a}} {{b}}", map[string]string{"a": "X"}, "X {{b}}", []string{"b"}},
		{"whitespace_in_braces", "{{ name }}", map[string]string{"name": "Bob"}, "Bob", nil},
		{"no_vars", "literal", map[string]string{"x": "1"}, "literal", nil},
		{"empty_template", "", map[string]string{"x": "1"}, "", nil},
		{"repeated_var", "{{x}} {{x}}", map[string]string{"x": "1"}, "1 1", nil},
		{"adjacent_vars", "{{a}}{{b}}", map[string]string{"a": "X", "b": "Y"}, "XY", nil},
		{"empty_value", "[{{x}}]", map[string]string{"x": ""}, "[]", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, missing := resolveTemplate(tt.tmpl, tt.vars)
			if result != tt.wantResult {
				t.Errorf("result = %q, want %q", result, tt.wantResult)
			}
			if len(missing) != len(tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
				return
			}
			for i := range missing {
				if missing[i] != tt.wantMissing[i] {
					t.Errorf("missing[%d] = %q, want %q", i, missing[i], tt.wantMissing[i])
				}
			}
		})
	}
}

func TestHasUnresolved(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"none", "plain text", nil},
		{"one", "{{foo}}", []string{"foo"}},
		{"multiple", "{{a}} {{b}}", []string{"a", "b"}},
		{"with_whitespace", "{{ x }}", []string{"x"}},
		{"empty", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasUnresolved(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{name: "basic", input: "echo test", want: []string{"echo", "test"}},
		{name: "quoted_spaces", input: `send --subject "{{_subject}}" --to "{{_address}}"`, want: []string{"send", "--subject", "{{_subject}}", "--to", "{{_address}}"}},
		{name: "empty_quoted_arg", input: `cmd "" value`, want: []string{"cmd", "", "value"}},
		{name: "unterminated_quote", input: `cmd "unterminated`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandLine(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildRecipients(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		headers := []string{"name", "email", "_status"}
		rows := [][]string{
			{"Alice", "alice@test.com", "Pending"},
			{"Bob", "bob@test.com", "Pending"},
		}
		recipients, errs := buildRecipients(headers, rows, 2, "{{email}}", "Hi {{name}}", "Body for {{name}}", "/tmp/f.txt", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(recipients) != 2 {
			t.Fatalf("got %d recipients, want 2", len(recipients))
		}
		if recipients[0].Address != "alice@test.com" {
			t.Errorf("address = %q, want alice@test.com", recipients[0].Address)
		}
		if recipients[0].Subject != "Hi Alice" {
			t.Errorf("subject = %q, want Hi Alice", recipients[0].Subject)
		}
		if recipients[0].Body != "Body for Alice" {
			t.Errorf("body = %q, want Body for Alice", recipients[0].Body)
		}
		if recipients[0].Command != "echo alice@test.com" {
			t.Errorf("command = %q, want echo alice@test.com", recipients[0].Command)
		}
		if recipients[0].Row != 0 || recipients[1].Row != 1 {
			t.Errorf("row indices wrong: %d, %d", recipients[0].Row, recipients[1].Row)
		}
	})

	t.Run("skips_sent", func(t *testing.T) {
		headers := []string{"email", "_status"}
		rows := [][]string{
			{"a@test.com", "Pending"},
			{"b@test.com", "Sent"},
			{"c@test.com", "Pending"},
		}
		recipients, errs := buildRecipients(headers, rows, 1, "{{email}}", "subj", "body", "/tmp/f", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(recipients) != 3 {
			t.Fatalf("got %d recipients, want 3", len(recipients))
		}
		if recipients[1].Status != "Sent" {
			t.Errorf("middle recipient status = %q, want Sent", recipients[1].Status)
		}
		if recipients[1].Address != "" {
			t.Errorf("sent recipient should have empty address, got %q", recipients[1].Address)
		}
		if recipients[0].Address != "a@test.com" {
			t.Errorf("first address = %q, want a@test.com", recipients[0].Address)
		}
	})

	t.Run("internal_var_precedence", func(t *testing.T) {
		headers := []string{"email", "_address", "_status"}
		rows := [][]string{
			{"real@test.com", "csv_val", "Pending"},
		}
		recipients, errs := buildRecipients(headers, rows, 2, "{{email}}", "subj", "body", "/tmp/f", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if recipients[0].Command != "echo real@test.com" {
			t.Errorf("command = %q, want 'echo real@test.com' (internal var should override CSV column)", recipients[0].Command)
		}
	})

	t.Run("missing_var_errors", func(t *testing.T) {
		headers := []string{"email", "_status"}
		rows := [][]string{
			{"a@test.com", "Pending"},
		}
		_, errs := buildRecipients(headers, rows, 1, "{{nonexistent}}", "subj", "body", "/tmp/f", "echo {{_address}}")
		if len(errs) == 0 {
			t.Fatal("expected errors for missing variable")
		}
	})

	t.Run("all_sent", func(t *testing.T) {
		headers := []string{"email", "_status"}
		rows := [][]string{
			{"a@test.com", "Sent"},
			{"b@test.com", "Sent"},
		}
		recipients, errs := buildRecipients(headers, rows, 1, "{{email}}", "subj", "body", "/tmp/f", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		for _, r := range recipients {
			if r.Status != "Sent" {
				t.Errorf("expected all Sent, got %q", r.Status)
			}
		}
	})

	t.Run("preserves_skipped_status", func(t *testing.T) {
		headers := []string{"email", "_status"}
		rows := [][]string{
			{"a@test.com", "Skipped"},
		}
		recipients, errs := buildRecipients(headers, rows, 1, "{{email}}", "subj", "body", "", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(recipients) != 1 || recipients[0].Status != "Skipped" {
			t.Fatalf("got %+v, want skipped recipient", recipients)
		}
	})

	t.Run("quoted_command_template_preserves_spaces", func(t *testing.T) {
		headers := []string{"email", "name", "_status"}
		rows := [][]string{
			{"alice@test.com", "Alice Smith", "Pending"},
		}
		recipients, errs := buildRecipients(headers, rows, 2, "{{email}}", "Hello {{name}}", "body", "", `sendmail --subject "{{_subject}}" --to "{{_address}}"`)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		want := []string{"sendmail", "--subject", "Hello Alice Smith", "--to", "alice@test.com"}
		if len(recipients[0].CommandArgs) != len(want) {
			t.Fatalf("got %v, want %v", recipients[0].CommandArgs, want)
		}
		for i := range want {
			if recipients[0].CommandArgs[i] != want[i] {
				t.Errorf("CommandArgs[%d] = %q, want %q", i, recipients[0].CommandArgs[i], want[i])
			}
		}
	})

	t.Run("csv_value_cannot_inject_extra_argv_tokens", func(t *testing.T) {
		headers := []string{"email", "subject", "_status"}
		rows := [][]string{
			{`alice@test.com`, `hello" --bcc attacker@example.com "world`, "Pending"},
		}
		recipients, errs := buildRecipients(headers, rows, 2, "{{email}}", "{{subject}}", "body", "", `sendmail --subject "{{_subject}}" --to "{{_address}}"`)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		want := []string{
			"sendmail",
			"--subject",
			`hello" --bcc attacker@example.com "world`,
			"--to",
			"alice@test.com",
		}
		if len(recipients[0].CommandArgs) != len(want) {
			t.Fatalf("got %v, want %v", recipients[0].CommandArgs, want)
		}
		for i := range want {
			if recipients[0].CommandArgs[i] != want[i] {
				t.Errorf("CommandArgs[%d] = %q, want %q", i, recipients[0].CommandArgs[i], want[i])
			}
		}
	})

	t.Run("short_rows_do_not_panic", func(t *testing.T) {
		headers := []string{"email", "_status"}
		rows := [][]string{
			{"a@test.com"},
		}
		recipients, errs := buildRecipients(headers, rows, 1, "{{email}}", "subj", "body", "", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(recipients) != 1 || recipients[0].Status != "Pending" {
			t.Fatalf("got %+v, want pending recipient", recipients)
		}
	})

	t.Run("no_rows", func(t *testing.T) {
		headers := []string{"email", "_status"}
		recipients, errs := buildRecipients(headers, nil, 1, "{{email}}", "subj", "body", "/tmp/f", "echo {{_address}}")
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(recipients) != 0 {
			t.Errorf("got %d recipients, want 0", len(recipients))
		}
	})
}
