package main

import (
	"encoding/csv"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"strings"
)

type AppData struct {
	Headers    []string
	Rows       [][]string
	StatusCol  int
	Recipients []Recipient
	CSVPath    string
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return s, nil
}

func readTemplate(path string) (string, error) {
	s, err := readFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func readBodyTemplate(path string) (string, error) {
	s, err := readFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.TrimLeft(s, " \t\r\n"), " \t\r\n"), nil
}

func loadCSV(path string) ([]string, [][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("%s is empty (no headers found)", path)
	}

	headers := records[0]
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}

	rows := records[1:]
	for i := range rows {
		for j := range rows[i] {
			rows[i][j] = strings.TrimSpace(rows[i][j])
		}
	}

	return headers, rows, nil
}

func saveCSV(path string, headers []string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write(headers); err != nil {
		return err
	}
	return w.WriteAll(rows)
}

func indexOf(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

func validate(recipients []Recipient) []string {
	var errs []string
	seenExec := make(map[string]bool)

	for _, r := range recipients {
		if r.Status == "Sent" || r.Status == "Skipped" {
			continue
		}

		if strings.TrimSpace(r.Address) == "" {
			errs = append(errs, fmt.Sprintf("  Row %d: Invalid email address %q\n    → Ensure the address is a valid non-empty email", r.Row+1, r.Address))
		} else {
			for _, addr := range strings.Split(r.Address, ",") {
				addr = strings.TrimSpace(addr)
				if addr == "" {
					continue
				}
				if _, err := mail.ParseAddress(addr); err != nil {
					errs = append(errs, fmt.Sprintf("  Row %d: Invalid email address %q in %q\n    → Ensure the address is a valid non-empty email", r.Row+1, addr, r.Address))
				}
			}
		}

		info, err := os.Stat(r.Attach)
		if err != nil {
			errs = append(errs, fmt.Sprintf("  Row %d: Attachment file not found: %q\n    → Check that the file exists at the specified path", r.Row+1, r.Attach))
		} else if info.IsDir() {
			errs = append(errs, fmt.Sprintf("  Row %d: Attachment path is a directory: %q\n    → Provide a path to a file, not a directory", r.Row+1, r.Attach))
		}

		parts := strings.Fields(r.Command)
		if len(parts) == 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Command is empty\n    → Check executable_commandline_template.txt", r.Row+1))
		} else {
			exe := parts[0]
			if !seenExec[exe] {
				if _, err := exec.LookPath(exe); err != nil {
					errs = append(errs, fmt.Sprintf("  Row %d: Command %q not found on PATH\n    → Verify the command name in executable_commandline_template.txt", r.Row+1, exe))
				}
				seenExec[exe] = true
			}
		}

		if unresolved := hasUnresolved(r.Command); len(unresolved) > 0 {
			errs = append(errs, fmt.Sprintf("  Row %d: Unresolved variables in final command: %s\n    → Ensure these columns exist in the CSV", r.Row+1, strings.Join(unresolved, ", ")))
		}
	}

	return errs
}

func main() {
	csvPath := "email_recipients.csv"

	addrTmpl, err := readTemplate("email_address_template.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	subjectTmpl, err := readTemplate("email_subject_template.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	bodyTmpl, err := readBodyTemplate("email_body_template.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	attachTmpl, err := readTemplate("email_attachment_template.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cmdTmpl, err := readTemplate("executable_commandline_template.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	headers, rows, err := loadCSV(csvPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(rows) == 0 {
		fmt.Println("No recipients found in CSV.")
		os.Exit(0)
	}

	statusCol := indexOf(headers, "_status")
	if statusCol == -1 {
		headers = append(headers, "_status")
		statusCol = len(headers) - 1
		for i := range rows {
			rows[i] = append(rows[i], "Pending")
		}
		if err := saveCSV(csvPath, headers, rows); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot write _status column to CSV: %v\n", err)
			os.Exit(1)
		}
	}

	recipients, buildErrs := buildRecipients(headers, rows, statusCol, addrTmpl, subjectTmpl, bodyTmpl, attachTmpl, cmdTmpl)
	if len(buildErrs) > 0 {
		fmt.Fprintf(os.Stderr, "Template resolution errors:\n\n%s\n", strings.Join(buildErrs, "\n\n"))
		os.Exit(1)
	}

	valErrs := validate(recipients)
	if len(valErrs) > 0 {
		fmt.Fprintf(os.Stderr, "Validation errors:\n\n%s\n", strings.Join(valErrs, "\n\n"))
		os.Exit(1)
	}

	var pending []int
	sentEver := 0
	for i, r := range recipients {
		if r.Status == "Sent" {
			sentEver++
		} else if r.Status == "Pending" {
			pending = append(pending, i)
		}
	}

	if len(pending) == 0 {
		fmt.Printf("All %d emails already processed.\n", len(recipients))
		os.Exit(0)
	}

	app := &AppData{
		Headers:    headers,
		Rows:       rows,
		StatusCol:  statusCol,
		Recipients: recipients,
		CSVPath:    csvPath,
	}

	summary := runServer(app, pending, sentEver)
	total := len(recipients)
	remaining := total - summary.SentEver - summary.SkippedRun
	fmt.Printf("\nSummary:\n  Total emails:     %d\n  Sent (all time):  %d\n  Sent (this run):  %d\n  Skipped:          %d\n  Remaining:        %d\n", total, summary.SentEver, summary.SentRun, summary.SkippedRun, remaining)
}
