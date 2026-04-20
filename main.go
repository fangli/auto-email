package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type AppData struct {
	Headers    []string
	Rows       [][]string
	StatusCol  int
	Recipients []Recipient
	CSVPath    string
	BaseDir    string
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
		if len(rows[i]) < len(headers) {
			rows[i] = append(rows[i], make([]string, len(headers)-len(rows[i]))...)
		}
		for j := range rows[i] {
			rows[i][j] = strings.TrimSpace(rows[i][j])
		}
	}

	return headers, rows, nil
}

func saveCSV(path string, headers []string, rows [][]string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	w := csv.NewWriter(tmp)
	if err := w.Write(headers); err != nil {
		tmp.Close()
		return err
	}
	if err := w.WriteAll(rows); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func indexOf(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

func resolveAttachmentPath(baseDir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	originalBaseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve base directory: %w", err)
	}
	baseAbs := originalBaseAbs
	if realBase, err := filepath.EvalSymlinks(baseAbs); err == nil {
		baseAbs = realBase
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("cannot evaluate base directory: %w", err)
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseAbs, candidate)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("cannot resolve attachment path: %w", err)
	}
	if realPath, err := filepath.EvalSymlinks(candidateAbs); err == nil {
		candidateAbs = realPath
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("cannot evaluate attachment path: %w", err)
	} else if filepath.IsAbs(path) {
		if relToOriginalBase, relErr := filepath.Rel(originalBaseAbs, candidateAbs); relErr == nil && relToOriginalBase != ".." && !strings.HasPrefix(relToOriginalBase, ".."+string(filepath.Separator)) {
			candidateAbs = filepath.Join(baseAbs, relToOriginalBase)
		}
	}

	rel, err := filepath.Rel(baseAbs, candidateAbs)
	if err != nil {
		return "", fmt.Errorf("cannot compare attachment path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("attachment path %q escapes %q", path, baseAbs)
	}
	return candidateAbs, nil
}

func validate(baseDir string, recipients []Recipient) []string {
	var errs []string
	seenExec := make(map[string]bool)

	for i := range recipients {
		r := &recipients[i]
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

		if strings.TrimSpace(r.Attach) != "" {
			resolvedAttach, err := resolveAttachmentPath(baseDir, r.Attach)
			if err != nil {
				errs = append(errs, fmt.Sprintf("  Row %d: Attachment path is outside the CSV workspace: %q\n    → Keep attachments under %s", r.Row+1, r.Attach, baseDir))
			} else {
				info, err := os.Stat(resolvedAttach)
				if err != nil {
					errs = append(errs, fmt.Sprintf("  Row %d: Attachment file not found: %q\n    → Check that the file exists at the specified path", r.Row+1, r.Attach))
				} else if info.IsDir() {
					errs = append(errs, fmt.Sprintf("  Row %d: Attachment path is a directory: %q\n    → Provide a path to a file, not a directory", r.Row+1, r.Attach))
				} else {
					r.AttachPath = resolvedAttach
				}
			}
		}

		parts := r.CommandArgs
		if len(parts) == 0 && strings.TrimSpace(r.Command) != "" {
			parsed, err := splitCommandLine(r.Command)
			if err != nil {
				errs = append(errs, fmt.Sprintf("  Row %d: Invalid command line %q\n    → Fix executable_commandline_template.txt quoting/escaping", r.Row+1, r.Command))
			} else {
				parts = parsed
				r.CommandArgs = parsed
			}
		}
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
	csvPath, err := filepath.Abs("email_recipients.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve CSV path: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Dir(csvPath)

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

	valErrs := validate(baseDir, recipients)
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
		BaseDir:    baseDir,
	}

	summary := runServer(app, pending, sentEver)
	total := len(recipients)
	remaining := 0
	for _, row := range app.Rows {
		if statusCol < len(row) && row[statusCol] == "Pending" {
			remaining++
		}
	}
	fmt.Printf("\nSummary:\n  Total emails:     %d\n  Sent (all time):  %d\n  Sent (this run):  %d\n  Skipped:          %d\n  Remaining:        %d\n", total, summary.SentEver, summary.SentRun, summary.SkippedRun, remaining)
}
