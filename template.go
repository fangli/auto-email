package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var tmplRe = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)

func resolveTemplate(tmpl string, vars map[string]string) (string, []string) {
	var missing []string
	result := tmplRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := tmplRe.FindStringSubmatch(match)[1]
		if val, ok := vars[name]; ok {
			return val
		}
		missing = append(missing, name)
		return match
	})
	return result, missing
}

func hasUnresolved(s string) []string {
	matches := tmplRe.FindAllStringSubmatch(s, -1)
	var names []string
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

type Recipient struct {
	Row         int
	Address     string
	Subject     string
	Body        string
	Attach      string
	AttachPath  string
	Command     string
	CommandArgs []string
	Status      string
}

func splitCommandLine(input string) ([]string, error) {
	var parts []string
	var current strings.Builder
	tokenStarted := false
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		if !tokenStarted {
			return
		}
		parts = append(parts, current.String())
		current.Reset()
		tokenStarted = false
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			tokenStarted = true
			escaped = false
		case inSingle:
			if r == '\'' {
				inSingle = false
				continue
			}
			current.WriteRune(r)
		case inDouble:
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
			}
		default:
			if unicode.IsSpace(r) {
				flush()
				continue
			}
			tokenStarted = true
			switch r {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
			}
		}
	}

	if escaped {
		return nil, fmt.Errorf("unterminated escape in command line")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in command line")
	}
	flush()
	return parts, nil
}

func buildRecipients(headers []string, rows [][]string, statusCol int, addrTmpl, subjectTmpl, bodyTmpl, attachTmpl, cmdTmpl string) ([]Recipient, []string) {
	var recipients []Recipient
	var errs []string
	cmdParts, cmdParseErr := splitCommandLine(cmdTmpl)

	for i, row := range rows {
		status := "Pending"
		if statusCol >= 0 && statusCol < len(row) {
			status = strings.TrimSpace(row[statusCol])
		}
		if status == "" {
			status = "Pending"
		}
		if status != "Pending" && status != "Sent" && status != "Skipped" {
			status = "Pending"
		}
		if status == "Sent" || status == "Skipped" {
			recipients = append(recipients, Recipient{Row: i, Status: status})
			continue
		}

		vars := make(map[string]string)
		for j, h := range headers {
			if j < len(row) {
				vars[h] = row[j]
			}
		}

		addr, missing := resolveTemplate(addrTmpl, vars)
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in address template: %s\n    → Ensure these columns exist in the CSV: %s", i+1, strings.Join(missing, ", "), strings.Join(missing, ", ")))
		}

		subject, missing := resolveTemplate(subjectTmpl, vars)
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in subject template: %s\n    → Ensure these columns exist in the CSV: %s", i+1, strings.Join(missing, ", "), strings.Join(missing, ", ")))
		}

		body, missing := resolveTemplate(bodyTmpl, vars)
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in body template: %s\n    → Ensure these columns exist in the CSV: %s", i+1, strings.Join(missing, ", "), strings.Join(missing, ", ")))
		}

		attach, missing := resolveTemplate(attachTmpl, vars)
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in attachment template: %s\n    → Ensure these columns exist in the CSV: %s", i+1, strings.Join(missing, ", "), strings.Join(missing, ", ")))
		}

		vars["_address"] = addr
		vars["_subject"] = subject
		vars["_body"] = body
		vars["_attachment"] = attach

		cmd, missing := resolveTemplate(cmdTmpl, vars)
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in command template: %s\n    → Ensure these columns exist in the CSV: %s", i+1, strings.Join(missing, ", "), strings.Join(missing, ", ")))
		}

		var resolvedCmdParts []string
		if cmdParseErr != nil {
			errs = append(errs, fmt.Sprintf("  Row %d: Invalid command template\n    → Fix executable_commandline_template.txt quoting/escaping: %v", i+1, cmdParseErr))
		} else {
			resolvedCmdParts = make([]string, 0, len(cmdParts))
			for _, part := range cmdParts {
				resolvedPart, _ := resolveTemplate(part, vars)
				resolvedCmdParts = append(resolvedCmdParts, resolvedPart)
			}
		}

		recipients = append(recipients, Recipient{
			Row:         i,
			Address:     addr,
			Subject:     subject,
			Body:        body,
			Attach:      attach,
			Command:     cmd,
			CommandArgs: resolvedCmdParts,
			Status:      status,
		})
	}

	return recipients, errs
}
