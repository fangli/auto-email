package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func makeTestApp(t *testing.T, recipients []Recipient) (*AppData, string) {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "test.csv")

	headers := []string{"email", "_status"}
	var rows [][]string
	for _, r := range recipients {
		rows = append(rows, []string{r.Address, r.Status})
	}
	if err := saveCSV(csvPath, headers, rows); err != nil {
		t.Fatal(err)
	}

	return &AppData{
		Headers:    headers,
		Rows:       rows,
		StatusCol:  1,
		Recipients: recipients,
		CSVPath:    csvPath,
	}, dir
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func enterKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func escKey() tea.KeyPressMsg    { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func ctrlCKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func testRecipient(attach string) Recipient {
	return Recipient{
		Row: 0, Address: "alice@test.com", Subject: "Hello",
		Body: "Test body", Attach: attach, Command: "echo test", Status: "Pending",
	}
}

func testRecipientN(n int, attach string) Recipient {
	return Recipient{
		Row: n, Address: fmt.Sprintf("user%d@test.com", n), Subject: fmt.Sprintf("Subject %d", n),
		Body: "Body", Attach: attach, Command: "echo test", Status: "Pending",
	}
}

// --- Key Sanity ---

func TestKeyPressStringValues(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"enter", enterKey(), "enter"},
		{"esc", escKey(), "esc"},
		{"ctrl_c", ctrlCKey(), "ctrl+c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- State Transitions ---

func TestPreviewEnterToSending(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	r := testRecipient(attach)
	app, _ := makeTestApp(t, []Recipient{r})
	m := newModel(app, []int{0}, 0)

	result, cmd := m.Update(enterKey())
	rm := result.(model)
	if rm.state != stateSending {
		t.Errorf("state = %d, want stateSending", rm.state)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd")
	}
}

func TestPreviewEscQuits(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	_, cmd := m.Update(escKey())
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}
}

func TestPreviewCtrlCQuits(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	_, cmd := m.Update(ctrlCKey())
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}
}

func TestSendSuccessToSent(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	r := testRecipient(attach)
	app, _ := makeTestApp(t, []Recipient{r})
	m := newModel(app, []int{0}, 0)
	m.state = stateSending

	result, cmd := m.Update(sendResultMsg{nil})
	rm := result.(model)
	if rm.state != stateSent {
		t.Errorf("state = %d, want stateSent", rm.state)
	}
	if rm.sentRun != 1 {
		t.Errorf("sentRun = %d, want 1", rm.sentRun)
	}
	if rm.sentEver != 1 {
		t.Errorf("sentEver = %d, want 1", rm.sentEver)
	}
	if rm.app.Rows[0][rm.app.StatusCol] != "Sent" {
		t.Errorf("CSV row not updated to Sent")
	}
	if cmd == nil {
		t.Error("expected tick cmd")
	}

	_, rows, err := loadCSV(app.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][1] != "Sent" {
		t.Errorf("CSV on disk = %q, want Sent", rows[0][1])
	}
}

func TestSendErrorToError(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateSending

	result, cmd := m.Update(sendResultMsg{fmt.Errorf("connection refused")})
	rm := result.(model)
	if rm.state != stateError {
		t.Errorf("state = %d, want stateError", rm.state)
	}
	if rm.err == nil || !strings.Contains(rm.err.Error(), "connection refused") {
		t.Errorf("err = %v, want connection refused", rm.err)
	}
	if cmd != nil {
		t.Error("expected nil cmd on error")
	}
	if rm.sentRun != 0 {
		t.Errorf("sentRun = %d, want 0", rm.sentRun)
	}
}

func TestSendSuccessCSVWriteFails(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	r := testRecipient(attach)
	app, _ := makeTestApp(t, []Recipient{r})
	app.CSVPath = "/dev/null/impossible"
	m := newModel(app, []int{0}, 0)
	m.state = stateSending

	result, cmd := m.Update(sendResultMsg{nil})
	rm := result.(model)
	if rm.state != stateError {
		t.Errorf("state = %d, want stateError", rm.state)
	}
	if rm.err == nil || !strings.Contains(rm.err.Error(), "failed to update CSV") {
		t.Errorf("err = %v, want 'failed to update CSV'", rm.err)
	}
	if rm.sentRun != 0 {
		t.Errorf("sentRun = %d, want 0 (increment is after CSV save)", rm.sentRun)
	}
	if cmd != nil {
		t.Error("expected nil cmd")
	}
}

func TestErrorEnterRetries(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateError
	m.err = fmt.Errorf("previous error")

	result, cmd := m.Update(enterKey())
	rm := result.(model)
	if rm.state != stateSending {
		t.Errorf("state = %d, want stateSending", rm.state)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for retry")
	}
}

func TestErrorEscQuits(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateError

	_, cmd := m.Update(escKey())
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}
}

func TestAutoAdvanceNextEntry(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := []Recipient{testRecipientN(0, attach), testRecipientN(1, attach), testRecipientN(2, attach)}
	app, _ := makeTestApp(t, rs)
	m := newModel(app, []int{0, 1, 2}, 0)
	m.state = stateSent
	m.cursor = 0

	result, cmd := m.Update(autoAdvanceMsg{})
	rm := result.(model)
	if rm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", rm.cursor)
	}
	if rm.state != statePreview {
		t.Errorf("state = %d, want statePreview", rm.state)
	}
	if cmd != nil {
		t.Error("expected nil cmd")
	}
}

func TestAutoAdvanceLastEntryQuits(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := []Recipient{testRecipientN(0, attach), testRecipientN(1, attach)}
	app, _ := makeTestApp(t, rs)
	m := newModel(app, []int{0, 1}, 0)
	m.cursor = 1

	result, cmd := m.Update(autoAdvanceMsg{})
	rm := result.(model)
	if rm.cursor != 2 {
		t.Errorf("cursor = %d, want 2", rm.cursor)
	}
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}
}

func TestWindowSizeMsg(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	result, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	rm := result.(model)
	if rm.width != 120 || rm.height != 40 {
		t.Errorf("size = %dx%d, want 120x40", rm.width, rm.height)
	}
	if cmd != nil {
		t.Error("expected nil cmd")
	}
}

func TestKeyIgnoredDuringSending(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateSending

	result, cmd := m.Update(enterKey())
	rm := result.(model)
	if rm.state != stateSending {
		t.Errorf("state changed to %d, should stay stateSending", rm.state)
	}
	if cmd != nil {
		t.Error("expected nil cmd")
	}
}

// --- View Rendering ---

func TestViewCursorOutOfBounds(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.cursor = 1

	v := m.View()
	if v.Content != "" {
		t.Errorf("expected empty content for out-of-bounds cursor, got %q", v.Content)
	}
}

func TestViewPreviewContent(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	r := testRecipient(attach)
	app, _ := makeTestApp(t, []Recipient{r})
	m := newModel(app, []int{0}, 0)

	v := m.View()
	for _, want := range []string{"alice@test.com", "Hello", "Press Enter to send the email immediately", "Press ESC to cancel", "Email 1 of 1 pending"} {
		if !strings.Contains(v.Content, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestViewSendingContent(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateSending

	v := m.View()
	if !strings.Contains(v.Content, "Sending...") {
		t.Error("view missing 'Sending...'")
	}
}

func TestViewSentContent(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateSent

	v := m.View()
	if !strings.Contains(v.Content, "Sent Successfully") {
		t.Error("view missing 'Sent Successfully'")
	}
}

func TestViewErrorContent(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)
	m.state = stateError
	m.err = fmt.Errorf("timeout")

	v := m.View()
	for _, want := range []string{"Error: timeout", "Press Enter to retry sending", "Press ESC to abort"} {
		if !strings.Contains(v.Content, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestViewProgressCounter(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := make([]Recipient, 5)
	for i := range rs {
		rs[i] = testRecipientN(i, attach)
	}
	rs[2].Status = "Sent"
	rs[3].Status = "Sent"
	rs[4].Status = "Sent"
	app, _ := makeTestApp(t, rs)
	m := newModel(app, []int{0, 1}, 3)

	v := m.View()
	if !strings.Contains(v.Content, "Email 1 of 2 pending") {
		t.Errorf("view missing progress, got: %s", v.Content)
	}
	if !strings.Contains(v.Content, "3/5 sent overall") {
		t.Errorf("view missing overall count, got: %s", v.Content)
	}
}

func TestViewAltScreen(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	v := m.View()
	if !v.AltScreen {
		t.Error("AltScreen should be true")
	}
}

// --- Full Flow Simulations ---

func TestFullFlowTwoRecipientsSendAll(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := []Recipient{testRecipientN(0, attach), testRecipientN(1, attach)}
	app, _ := makeTestApp(t, rs)
	m := newModel(app, []int{0, 1}, 0)

	if m.state != statePreview || m.cursor != 0 {
		t.Fatalf("initial: state=%d cursor=%d", m.state, m.cursor)
	}

	// 1. Enter → sending
	result, cmd := m.Update(enterKey())
	m = result.(model)
	if m.state != stateSending || cmd == nil {
		t.Fatalf("step1: state=%d cmd=%v", m.state, cmd)
	}

	// 2. Send success → sent
	result, cmd = m.Update(sendResultMsg{nil})
	m = result.(model)
	if m.state != stateSent || m.sentRun != 1 || m.sentEver != 1 {
		t.Fatalf("step2: state=%d sentRun=%d sentEver=%d", m.state, m.sentRun, m.sentEver)
	}

	// 3. Auto-advance → next entry
	result, cmd = m.Update(autoAdvanceMsg{})
	m = result.(model)
	if m.cursor != 1 || m.state != statePreview {
		t.Fatalf("step3: cursor=%d state=%d", m.cursor, m.state)
	}

	// 4. Enter → sending
	result, cmd = m.Update(enterKey())
	m = result.(model)
	if m.state != stateSending {
		t.Fatalf("step4: state=%d", m.state)
	}

	// 5. Send success → sent
	result, cmd = m.Update(sendResultMsg{nil})
	m = result.(model)
	if m.state != stateSent || m.sentRun != 2 || m.sentEver != 2 {
		t.Fatalf("step5: state=%d sentRun=%d sentEver=%d", m.state, m.sentRun, m.sentEver)
	}

	// 6. Auto-advance past end → quit
	result, cmd = m.Update(autoAdvanceMsg{})
	m = result.(model)
	if m.cursor != 2 {
		t.Fatalf("step6: cursor=%d", m.cursor)
	}
	if !isQuitCmd(cmd) {
		t.Fatal("step6: expected quit cmd")
	}

	// 7. Verify CSV on disk
	_, rows, err := loadCSV(app.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if row[1] != "Sent" {
			t.Errorf("row %d status = %q, want Sent", i, row[1])
		}
	}
}

func TestFullFlowErrorThenRetry(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	// Enter → sending
	result, _ := m.Update(enterKey())
	m = result.(model)

	// Error
	result, _ = m.Update(sendResultMsg{fmt.Errorf("fail")})
	m = result.(model)
	if m.state != stateError {
		t.Fatalf("expected stateError, got %d", m.state)
	}

	// Retry
	result, cmd := m.Update(enterKey())
	m = result.(model)
	if m.state != stateSending || cmd == nil {
		t.Fatalf("retry: state=%d cmd=%v", m.state, cmd)
	}

	// Success
	result, _ = m.Update(sendResultMsg{nil})
	m = result.(model)
	if m.sentRun != 1 {
		t.Errorf("sentRun = %d, want 1", m.sentRun)
	}

	// Auto-advance → quit
	_, cmd = m.Update(autoAdvanceMsg{})
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}

	_, rows, _ := loadCSV(app.CSVPath)
	if rows[0][1] != "Sent" {
		t.Errorf("CSV status = %q, want Sent", rows[0][1])
	}
}

func TestFullFlowEscFromPreview(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := []Recipient{testRecipientN(0, attach), testRecipientN(1, attach)}
	app, _ := makeTestApp(t, rs)
	m := newModel(app, []int{0, 1}, 0)

	result, cmd := m.Update(escKey())
	rm := result.(model)
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}
	if rm.sentRun != 0 {
		t.Errorf("sentRun = %d, want 0", rm.sentRun)
	}

	_, rows, _ := loadCSV(app.CSVPath)
	for i, row := range rows {
		if row[1] != "Pending" {
			t.Errorf("row %d status = %q, want Pending", i, row[1])
		}
	}
}

func TestFullFlowEscFromError(t *testing.T) {
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	m := newModel(app, []int{0}, 0)

	// Enter → sending
	result, _ := m.Update(enterKey())
	m = result.(model)

	// Error
	result, _ = m.Update(sendResultMsg{fmt.Errorf("fail")})
	m = result.(model)

	// ESC → quit
	_, cmd := m.Update(escKey())
	if !isQuitCmd(cmd) {
		t.Error("expected quit cmd")
	}

	_, rows, _ := loadCSV(app.CSVPath)
	if rows[0][1] != "Pending" {
		t.Errorf("CSV status = %q, want Pending", rows[0][1])
	}
}
