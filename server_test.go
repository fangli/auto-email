package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func makeTestApp(t *testing.T, recipients []Recipient) (*AppData, string) {
	t.Helper()
	dir := t.TempDir()
	for _, r := range recipients {
		if len(r.Attachments) > 0 {
			dir = filepath.Dir(r.Attachments[0])
			break
		}
	}
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
		BaseDir:    dir,
	}, dir
}

func testRecipient(attachments ...string) Recipient {
	return Recipient{
		Row: 0, Address: "alice@test.com", Subject: "Hello",
		Body: "Test body", Attachments: attachments, Status: "Pending",
	}
}

func testRecipientN(n int, attachments ...string) Recipient {
	return Recipient{
		Row: n, Address: fmt.Sprintf("user%d@test.com", n), Subject: fmt.Sprintf("Subject %d", n),
		Body: "Body", Attachments: attachments, Status: "Pending",
	}
}

func setupSingleTest(t *testing.T) (*serverState, http.Handler) {
	t.Helper()
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(attach)})
	s := newState(app, []int{0}, 0)
	return s, newTestServer(t, s)
}

func setupMultiTest(t *testing.T) (*serverState, http.Handler, string) {
	t.Helper()
	attach := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(attach, []byte("x"), 0644)
	rs := []Recipient{testRecipientN(0, attach), testRecipientN(1, attach)}
	app, _ := makeTestApp(t, rs)
	s := newState(app, []int{0, 1}, 0)
	return s, newTestServer(t, s), attach
}

func minimalPDF(text string) []byte {
	streamContent := fmt.Sprintf("BT /F1 12 Tf (%s) Tj ET", text)
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.0\n")
	obj1 := buf.Len()
	buf.WriteString("1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n")
	obj2 := buf.Len()
	buf.WriteString("2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n")
	obj3 := buf.Len()
	buf.WriteString("3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>>>>>>>endobj\n")
	obj4 := buf.Len()
	buf.WriteString(fmt.Sprintf("4 0 obj<</Length %d>>\nstream\n%s\nendstream\nendobj\n", len(streamContent), streamContent))
	xref := buf.Len()
	buf.WriteString("xref\n0 5\n")
	buf.WriteString("0000000000 65535 f \n")
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj1))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj2))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj3))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj4))
	buf.WriteString(fmt.Sprintf("trailer<</Size 5/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF", xref))
	return buf.Bytes()
}

func minimalDocx(text string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("word/document.xml")
	fmt.Fprintf(f, `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>%s</w:t></w:r></w:p></w:body></w:document>`, text)
	w.Close()
	return buf.Bytes()
}

func newTestServer(t *testing.T, s *serverState) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/send", s.handleSend)
	mux.HandleFunc("POST /api/skip", s.handleSkip)
	mux.HandleFunc("GET /api/attachment", s.handleAttachment)
	mux.HandleFunc("GET /api/preview", s.handlePreview)
	mux.HandleFunc("GET /events", s.handleEvents)
	return mux
}

func newState(app *AppData, pending []int, sentEver int) *serverState {
	ctx, cancel := context.WithCancel(context.Background())
	return &serverState{
		app:      app,
		pending:  pending,
		cursor:   0,
		state:    "preview",
		sentEver: sentEver,
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		sendCmdFunc: func(ctx context.Context, rec Recipient, baseDir string) (string, error) {
			return "", nil
		},
	}
}

func doRequest(t *testing.T, handler http.Handler, method, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Result()
}

func doRequestBody(t *testing.T, handler http.Handler, method, path string, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Result()
}

func getStatus(t *testing.T, ts http.Handler) statusResponse {
	t.Helper()
	resp := doRequest(t, ts, http.MethodGet, "/api/status")
	defer resp.Body.Close()
	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatal(err)
	}
	return sr
}

func postAction(t *testing.T, ts http.Handler, path string) *http.Response {
	t.Helper()
	return doRequest(t, ts, http.MethodPost, path)
}

func waitForState(t *testing.T, ts http.Handler, want string, timeout time.Duration) statusResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sr := getStatus(t, ts)
		if sr.State == want {
			return sr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %q", want)
	return statusResponse{}
}

type sseTestWriter struct {
	mu         sync.Mutex
	header     http.Header
	body       bytes.Buffer
	status     int
	firstWrite chan struct{}
	once       sync.Once
}

func newSSETestWriter() *sseTestWriter {
	return &sseTestWriter{
		header:     make(http.Header),
		firstWrite: make(chan struct{}),
	}
}

func (w *sseTestWriter) Header() http.Header {
	return w.header
}

func (w *sseTestWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = statusCode
}

func (w *sseTestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.body.Write(p)
	if n > 0 {
		w.once.Do(func() { close(w.firstWrite) })
	}
	return n, err
}

func (w *sseTestWriter) Flush() {}

func (w *sseTestWriter) BodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// --- Status Endpoint ---

func TestStatusEndpoint(t *testing.T) {
	_, ts := setupSingleTest(t)

	sr := getStatus(t, ts)
	if sr.State != "preview" {
		t.Errorf("state = %q, want preview", sr.State)
	}
	if sr.Recipient == nil {
		t.Fatal("recipient is nil")
	}
	if sr.Recipient.Address != "alice@test.com" {
		t.Errorf("address = %q", sr.Recipient.Address)
	}
	if sr.Progress.Current != 1 || sr.Progress.Pending != 1 || sr.Progress.Total != 1 {
		t.Errorf("progress = %+v", sr.Progress)
	}
}

// --- Send Transitions ---

func TestSendTransition(t *testing.T) {
	_, ts := setupSingleTest(t)

	resp := postAction(t, ts, "/api/send")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}

	sr := waitForState(t, ts, "sent", 5*time.Second)
	if sr.Progress.SentRun != 1 || sr.Progress.SentEver != 1 {
		t.Errorf("progress = %+v", sr.Progress)
	}
}

func TestSendSuccess(t *testing.T) {
	s, ts := setupSingleTest(t)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)

	_, rows, err := loadCSV(s.app.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][1] != "Sent" {
		t.Errorf("CSV status = %q, want Sent", rows[0][1])
	}
}

func TestSendError(t *testing.T) {
	s, ts := setupSingleTest(t)
	s.sendCmdFunc = func(ctx context.Context, rec Recipient, baseDir string) (string, error) {
		return "", fmt.Errorf("send failed")
	}

	postAction(t, ts, "/api/send").Body.Close()
	sr := waitForState(t, ts, "error", 5*time.Second)
	if sr.Error == "" {
		t.Error("expected non-empty error")
	}
}

func TestFormatCommandError(t *testing.T) {
	if got := formatCommandError(fmt.Errorf("boom"), ""); got != "boom" {
		t.Errorf("got %q, want %q", got, "boom")
	}
	if got := formatCommandError(fmt.Errorf("boom"), "details"); got != "boom\ndetails" {
		t.Errorf("got %q, want %q", got, "boom\ndetails")
	}
}

func TestSendCSVWriteFailure(t *testing.T) {
	s, ts := setupSingleTest(t)
	s.app.CSVPath = "/dev/null/impossible"

	postAction(t, ts, "/api/send").Body.Close()
	sr := waitForState(t, ts, "error", 5*time.Second)
	if !strings.Contains(sr.Error, "failed to update CSV") {
		t.Errorf("error = %q, want 'failed to update CSV'", sr.Error)
	}
}

func TestRetryAfterError(t *testing.T) {
	s, ts := setupSingleTest(t)
	s.state = "error"
	s.lastError = "previous error"

	resp := postAction(t, ts, "/api/send")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}

	waitForState(t, ts, "sent", 5*time.Second)
}

func TestSkipWhileSentConflicts(t *testing.T) {
	s, ts := setupSingleTest(t)
	s.state = "sent"

	resp := postAction(t, ts, "/api/skip")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestSendWhileSending409(t *testing.T) {
	s, ts := setupSingleTest(t)
	s.sendCmdFunc = func(ctx context.Context, rec Recipient, baseDir string) (string, error) {
		time.Sleep(2 * time.Second)
		return "", nil
	}

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sending", 2*time.Second)

	resp := postAction(t, ts, "/api/send")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestSendRejectsRequestBody(t *testing.T) {
	_, ts := setupSingleTest(t)

	resp := doRequestBody(t, ts, http.MethodPost, "/api/send", []byte("x"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSkipRejectsLargeRequestBody(t *testing.T) {
	_, ts := setupSingleTest(t)

	resp := doRequestBody(t, ts, http.MethodPost, "/api/skip", []byte("too much"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

// --- Skip ---

func TestSkipAdvances(t *testing.T) {
	s, ts, _ := setupMultiTest(t)

	postAction(t, ts, "/api/skip").Body.Close()
	sr := getStatus(t, ts)
	if sr.State != "preview" {
		t.Errorf("state = %q, want preview", sr.State)
	}
	if sr.Progress.Current != 2 {
		t.Errorf("current = %d, want 2", sr.Progress.Current)
	}

	_, rows, _ := loadCSV(s.app.CSVPath)
	if rows[0][1] != "Skipped" {
		t.Errorf("row 0 status = %q, want Skipped", rows[0][1])
	}
}

func TestSkipLastToDone(t *testing.T) {
	s, ts := setupSingleTest(t)

	postAction(t, ts, "/api/skip").Body.Close()
	sr := getStatus(t, ts)
	if sr.State != "done" {
		t.Errorf("state = %q, want done", sr.State)
	}

	_, rows, _ := loadCSV(s.app.CSVPath)
	if rows[0][1] != "Skipped" {
		t.Errorf("row 0 status = %q, want Skipped", rows[0][1])
	}

	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("done channel not closed after skip-to-done")
	}
}

// --- Auto-advance ---

func TestAutoAdvance(t *testing.T) {
	_, ts, _ := setupMultiTest(t)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)

	sr := waitForState(t, ts, "preview", 5*time.Second)
	if sr.Progress.Current != 2 {
		t.Errorf("current = %d, want 2", sr.Progress.Current)
	}
	if sr.Recipient.Address != "user1@test.com" {
		t.Errorf("address = %q, want user1@test.com", sr.Recipient.Address)
	}
}

func TestAutoAdvanceDone(t *testing.T) {
	_, ts := setupSingleTest(t)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)
	waitForState(t, ts, "done", 5*time.Second)
}

// --- Attachment & Preview Endpoints ---

func TestAttachmentEndpoint(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.pdf")
	os.WriteFile(f, minimalPDF("Hello"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(f)})
	s := newState(app, []int{0}, 0)
	ts := newTestServer(t, s)

	resp := doRequest(t, ts, http.MethodGet, "/api/attachment")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "pdf") && !strings.Contains(ct, "octet-stream") {
		t.Errorf("content-type = %q", ct)
	}
}

// --- Preview Endpoints ---

func TestPreviewEndpointTxt(t *testing.T) {
	f := filepath.Join(t.TempDir(), "note.txt")
	os.WriteFile(f, []byte("Hello from txt"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(f)})
	s := newState(app, []int{0}, 0)
	ts := newTestServer(t, s)

	resp := doRequest(t, ts, http.MethodGet, "/api/preview")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "Hello from txt") {
		t.Errorf("body = %q", buf.String())
	}
}

func TestPreviewEndpointDocx204(t *testing.T) {
	f := filepath.Join(t.TempDir(), "doc.docx")
	os.WriteFile(f, minimalDocx("Hello from docx"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(f)})
	s := newState(app, []int{0}, 0)
	ts := newTestServer(t, s)

	resp := doRequest(t, ts, http.MethodGet, "/api/preview")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestPreviewEndpointPDF204(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.pdf")
	os.WriteFile(f, minimalPDF("Hello"), 0644)
	app, _ := makeTestApp(t, []Recipient{testRecipient(f)})
	s := newState(app, []int{0}, 0)
	ts := newTestServer(t, s)

	resp := doRequest(t, ts, http.MethodGet, "/api/preview")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

// --- Extract Preview Text (pure function tests) ---

func TestExtractPreviewText(t *testing.T) {
	t.Run("unsupported_ext_returns_empty", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.png")
		os.WriteFile(f, []byte("data"), 0644)
		if got := extractPreviewText(f); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
	t.Run("nonexistent_returns_empty", func(t *testing.T) {
		if got := extractPreviewText("/no/such/file.pdf"); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
	t.Run("valid_pdf_returns_text", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "test.pdf")
		os.WriteFile(f, minimalPDF("Hello World"), 0644)
		got := extractPreviewText(f)
		if !strings.Contains(got, "Hello") {
			t.Errorf("expected text containing 'Hello', got %q", got)
		}
	})
	t.Run("txt_returns_content", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "note.txt")
		os.WriteFile(f, []byte("Hello from txt"), 0644)
		got := extractPreviewText(f)
		if got != "Hello from txt" {
			t.Errorf("got %q, want 'Hello from txt'", got)
		}
	})
	t.Run("txt_large_is_truncated", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "large.txt")
		if err := os.WriteFile(f, bytes.Repeat([]byte("a"), maxPreviewBytes+128), 0644); err != nil {
			t.Fatal(err)
		}
		got := extractPreviewText(f)
		if !strings.Contains(got, "[Preview truncated]") {
			t.Fatalf("expected truncation marker, got %q", got[len(got)-min(len(got), 64):])
		}
		if len(got) > maxPreviewBytes+len("\n\n[Preview truncated]") {
			t.Errorf("preview length = %d, unexpectedly large", len(got))
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Full Flow ---

func TestFullFlowHTTP(t *testing.T) {
	s, ts, _ := setupMultiTest(t)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)
	waitForState(t, ts, "preview", 5*time.Second)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)
	waitForState(t, ts, "done", 5*time.Second)

	_, rows, err := loadCSV(s.app.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if row[1] != "Sent" {
			t.Errorf("row %d status = %q, want Sent", i, row[1])
		}
	}
}

func TestFullFlowErrorRetry(t *testing.T) {
	s, ts := setupSingleTest(t)
	callCount := 0
	s.sendCmdFunc = func(ctx context.Context, rec Recipient, baseDir string) (string, error) {
		callCount++
		if callCount == 1 {
			return "", fmt.Errorf("send failed")
		}
		return "", nil
	}

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "error", 5*time.Second)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)

	_, rows, _ := loadCSV(s.app.CSVPath)
	if rows[0][1] != "Sent" {
		t.Errorf("CSV status = %q, want Sent", rows[0][1])
	}
}

// --- SSE ---

func TestSSEStream(t *testing.T) {
	_, ts := setupSingleTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rr := newSSETestWriter()
	done := make(chan struct{})
	go func() {
		ts.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-rr.firstWrite:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial SSE event")
	}
	cancel()
	<-done

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}

	line := rr.BodyString()
	if !strings.Contains(line, "preview") {
		t.Errorf("initial SSE missing 'preview': %s", line)
	}
}

// --- Dryrun ---

func setupDryrunTest(t *testing.T) (*serverState, http.Handler) {
	t.Helper()
	s, ts := setupSingleTest(t)
	s.dryrun = true
	s.sendCmdFunc = dryrunSendCmd
	return s, ts
}

func TestDryrunStatusFlag(t *testing.T) {
	_, ts := setupDryrunTest(t)
	sr := getStatus(t, ts)
	if !sr.Dryrun {
		t.Error("expected dryrun=true in status")
	}
}

func TestDryrunStatusFlagFalse(t *testing.T) {
	_, ts := setupSingleTest(t)
	sr := getStatus(t, ts)
	if sr.Dryrun {
		t.Error("expected dryrun=false in status")
	}
}

func TestDryrunSendUpdatesCSV(t *testing.T) {
	s, ts := setupDryrunTest(t)

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)

	_, rows, err := loadCSV(s.app.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][1] != "Sent" {
		t.Errorf("CSV status = %q, want Sent", rows[0][1])
	}
}

func TestDryrunNeverCallsDefaultSend(t *testing.T) {
	s, ts := setupDryrunTest(t)
	called := false
	s.sendCmdFunc = func(ctx context.Context, rec Recipient, baseDir string) (string, error) {
		called = true
		return dryrunSendCmd(ctx, rec, baseDir)
	}

	postAction(t, ts, "/api/send").Body.Close()
	waitForState(t, ts, "sent", 5*time.Second)

	if !called {
		t.Fatal("sendCmdFunc was not called at all")
	}
}

// --- Index ---

func TestIndexServesHTML(t *testing.T) {
	_, ts := setupSingleTest(t)

	resp := doRequest(t, ts, http.MethodGet, "/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}
