package main

import (
	"fmt"
	"regexp"
	"strings"
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
	Row     int
	Address string
	Subject string
	Body    string
	Attach  string
	Command string
	Status  string
}

func buildRecipients(headers []string, rows [][]string, statusCol int, addrTmpl, subjectTmpl, bodyTmpl, attachTmpl, cmdTmpl string) ([]Recipient, []string) {
	var recipients []Recipient
	var errs []string

	for i, row := range rows {
		status := strings.TrimSpace(row[statusCol])
		if status == "Sent" {
			recipients = append(recipients, Recipient{Row: i, Status: "Sent"})
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

		recipients = append(recipients, Recipient{
			Row:     i,
			Address: addr,
			Subject: subject,
			Body:    body,
			Attach:  attach,
			Command: cmd,
			Status:  "Pending",
		})
	}

	return recipients, errs
}
