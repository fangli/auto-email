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
	Row         int
	Address     string
	Subject     string
	Body        string
	Attachments []string
	Status      string
}

func splitAttachments(raw string) []string {
	var result []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func resolveField(tmpl string, vars map[string]string, row int, field string) (string, string) {
	result, missing := resolveTemplate(tmpl, vars)
	if len(missing) > 0 {
		return result, fmt.Sprintf("  Row %d: Unresolved variables in %s template: %s\n    → Ensure these columns exist in the CSV: %s", row+1, field, strings.Join(missing, ", "), strings.Join(missing, ", "))
	}
	return result, ""
}

func buildRecipients(headers []string, rows [][]string, statusCol int, addrTmpl, subjectTmpl, bodyTmpl, attachTmpl string) ([]Recipient, []string) {
	var recipients []Recipient
	var errs []string

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

		addr, errMsg := resolveField(addrTmpl, vars, i, "address")
		if errMsg != "" {
			errs = append(errs, errMsg)
		}
		subject, errMsg := resolveField(subjectTmpl, vars, i, "subject")
		if errMsg != "" {
			errs = append(errs, errMsg)
		}
		body, errMsg := resolveField(bodyTmpl, vars, i, "body")
		if errMsg != "" {
			errs = append(errs, errMsg)
		}
		attach, errMsg := resolveField(attachTmpl, vars, i, "attachment")
		if errMsg != "" {
			errs = append(errs, errMsg)
		}

		recipients = append(recipients, Recipient{
			Row:         i,
			Address:     addr,
			Subject:     subject,
			Body:        body,
			Attachments: splitAttachments(attach),
			Status:      status,
		})
	}

	return recipients, errs
}
