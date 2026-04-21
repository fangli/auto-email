package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html
var indexHTML []byte

type Summary struct {
	SentRun    int
	SkippedRun int
	SentEver   int
}

type serverState struct {
	mu          sync.Mutex
	app         *AppData
	pending     []int
	cursor      int
	state       string
	lastError   string
	sentRun     int
	skippedRun  int
	sentEver    int
	clients     []chan string
	done        chan struct{}
	doneOnce    sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	sendCmdFunc func(ctx context.Context, rec Recipient, baseDir string) (string, error)
	dryrun      bool
	csvMon      *csvMonitor
}

type csvMonitor struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	paused  bool
}

func newCSVMonitor(path string) *csvMonitor {
	info, _ := os.Stat(path)
	var mt time.Time
	if info != nil {
		mt = info.ModTime()
	}
	return &csvMonitor{path: path, modTime: mt}
}

func (m *csvMonitor) pause() { m.mu.Lock(); m.paused = true; m.mu.Unlock() }

func (m *csvMonitor) resume() {
	info, _ := os.Stat(m.path)
	m.mu.Lock()
	if info != nil {
		m.modTime = info.ModTime()
	}
	m.paused = false
	m.mu.Unlock()
}

func (m *csvMonitor) check() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused {
		return false
	}
	info, err := os.Stat(m.path)
	if err != nil {
		return false
	}
	return !info.ModTime().Equal(m.modTime)
}

func (s *serverState) safeWriteCSV() error {
	if s.csvMon != nil {
		s.csvMon.pause()
		defer s.csvMon.resume()
	}
	return saveCSV(s.app.CSVPath, s.app.Headers, s.app.Rows)
}

const (
	sendCommandTimeout    = 2 * time.Minute
	maxPreviewBytes       = 1 << 20
	maxCommandOutputBytes = 64 << 10
	maxControlBodyBytes   = 1
)

type limitedBuffer struct {
	limit int
	buf   bytes.Buffer
	total int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		if _, err := b.buf.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.total > b.buf.Len() {
		if s != "" {
			s += "\n"
		}
		s += "[output truncated]"
	}
	return s
}

func readLimitedBytes(r io.Reader, limit int64) ([]byte, bool, error) {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return data, truncated, nil
}

func appendPreviewTruncated(text string) string {
	text = strings.TrimSpace(text)
	if text != "" {
		text += "\n\n"
	}
	return text + "[Preview truncated]"
}

func readTextPreview(r io.Reader) string {
	data, truncated, err := readLimitedBytes(r, maxPreviewBytes)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if truncated {
		text = appendPreviewTruncated(text)
	}
	return text
}

func formatCommandError(err error, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v\n%s", err, output)
}

func runCommandCombinedLimited(cmd *exec.Cmd, limit int) (string, error) {
	var output limitedBuffer
	output.limit = limit
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

type statusResponse struct {
	State      string         `json:"state"`
	Recipient  *recipientJSON `json:"recipient,omitempty"`
	Progress   progressJSON   `json:"progress"`
	Error      string         `json:"error"`
	LoggedInAs string         `json:"loggedInAs"`
	Dryrun     bool           `json:"dryrun"`
}

type attachmentJSON struct {
	Path string `json:"path"`
	Ext  string `json:"ext"`
	Size string `json:"size"`
}

type recipientJSON struct {
	Address     string           `json:"address"`
	Subject     string           `json:"subject"`
	Body        string           `json:"body"`
	Attachments []attachmentJSON `json:"attachments"`
}

type progressJSON struct {
	Current    int `json:"current"`
	Pending    int `json:"pending"`
	SentRun    int `json:"sentRun"`
	SkippedRun int `json:"skippedRun"`
	SentEver   int `json:"sentEver"`
	Total      int `json:"total"`
}

type statusSnapshot struct {
	state      string
	lastError  string
	current    int
	pending    int
	sentRun    int
	skippedRun int
	sentEver   int
	total      int
	recipient  *Recipient
	baseDir    string
	loggedInAs string
	dryrun     bool
}

func extractPreviewText(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		return readTextPreview(f)
	}
	return ""
}

func fileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}
	size := info.Size()
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot open browser: %v\n", err)
		return
	}
	go cmd.Wait()
}

func resolveRelPath(baseDir, path string) string {
	if !filepath.IsAbs(path) && baseDir != "" {
		return filepath.Join(baseDir, path)
	}
	return path
}

func (s *serverState) currentRecipientLocked() (int, Recipient, bool) {
	if s.cursor < 0 || s.cursor >= len(s.pending) {
		return -1, Recipient{}, false
	}
	recIdx := s.pending[s.cursor]
	if recIdx < 0 || recIdx >= len(s.app.Recipients) {
		return -1, Recipient{}, false
	}
	return recIdx, s.app.Recipients[recIdx], true
}

func (s *serverState) currentRecipientForRequest() (Recipient, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "done" {
		return Recipient{}, false
	}
	_, rec, ok := s.currentRecipientLocked()
	return rec, ok
}

func (s *serverState) resolveAttachPath(rec Recipient, index int) (string, bool) {
	if index < 0 || index >= len(rec.Attachments) {
		return "", false
	}
	return resolveRelPath(s.app.BaseDir, rec.Attachments[index]), true
}

func (s *serverState) setErrorStateLocked(msg string) {
	s.state = "error"
	s.lastError = msg
	s.broadcastStateLocked()
}

func (s *serverState) requireActionableStateLocked() (int, Recipient, error) {
	if s.state != "preview" && s.state != "error" {
		return -1, Recipient{}, fmt.Errorf("not in actionable state")
	}
	recIdx, rec, ok := s.currentRecipientLocked()
	if !ok {
		s.state = "done"
		s.broadcastStateLocked()
		return -1, Recipient{}, fmt.Errorf("no current recipient")
	}
	return recIdx, rec, nil
}

func (s *serverState) handleAttachment(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.currentRecipientForRequest()
	if !ok || len(rec.Attachments) == 0 {
		http.NotFound(w, r)
		return
	}
	index, _ := strconv.Atoi(r.URL.Query().Get("index"))
	path, ok := s.resolveAttachPath(rec, index)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *serverState) handlePreview(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.currentRecipientForRequest()
	if !ok || len(rec.Attachments) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	index, _ := strconv.Atoi(r.URL.Query().Get("index"))
	path, ok := s.resolveAttachPath(rec, index)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".txt" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text := extractPreviewText(path)
	if text == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(text)); err != nil {
		return
	}
}

func (s *serverState) removeClientLocked(target chan string) {
	for i, ch := range s.clients {
		if ch == target {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

func writeSSEMessage(w http.ResponseWriter, flusher http.Flusher, msg string) bool {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (s *serverState) snapshotLocked() statusSnapshot {
	snapshot := statusSnapshot{
		state:      s.state,
		lastError:  s.lastError,
		current:    s.cursor + 1,
		pending:    len(s.pending),
		sentRun:    s.sentRun,
		skippedRun: s.skippedRun,
		sentEver:   s.sentEver,
		total:      len(s.app.Recipients),
		baseDir:    s.app.BaseDir,
		loggedInAs: s.app.LoggedInAs,
		dryrun:     s.dryrun,
	}
	if s.state != "done" {
		_, r, ok := s.currentRecipientLocked()
		if ok {
			recCopy := r
			snapshot.recipient = &recCopy
		}
	}
	return snapshot
}

func buildStatus(snapshot statusSnapshot) statusResponse {
	resp := statusResponse{
		State: snapshot.state,
		Progress: progressJSON{
			Current:    snapshot.current,
			Pending:    snapshot.pending,
			SentRun:    snapshot.sentRun,
			SkippedRun: snapshot.skippedRun,
			SentEver:   snapshot.sentEver,
			Total:      snapshot.total,
		},
		Error:      snapshot.lastError,
		LoggedInAs: snapshot.loggedInAs,
		Dryrun:     snapshot.dryrun,
	}
	if snapshot.recipient != nil {
		r := snapshot.recipient
		var attachments []attachmentJSON
		for _, a := range r.Attachments {
			path := resolveRelPath(snapshot.baseDir, a)
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(a)), ".")
			attachments = append(attachments, attachmentJSON{
				Path: a,
				Ext:  ext,
				Size: fileSize(path),
			})
		}
		resp.Recipient = &recipientJSON{
			Address:     r.Address,
			Subject:     r.Subject,
			Body:        r.Body,
			Attachments: attachments,
		}
	}
	return resp
}

func broadcastState(clients []chan string, resp statusResponse) []chan string {
	data, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	msg := string(data)
	var stale []chan string
	for _, ch := range clients {
		select {
		case ch <- msg:
		default:
			stale = append(stale, ch)
		}
	}
	return stale
}

func (s *serverState) broadcastStateLocked() {
	snapshot := s.snapshotLocked()
	clients := append([]chan string(nil), s.clients...)
	s.mu.Unlock()
	stale := broadcastState(clients, buildStatus(snapshot))
	s.mu.Lock()
	for _, ch := range stale {
		s.removeClientLocked(ch)
	}
}

func (s *serverState) advance() {
	s.cursor++
	if s.cursor >= len(s.pending) {
		s.state = "done"
		s.broadcastStateLocked()
		time.AfterFunc(2*time.Second, func() {
			s.signalDone()
		})
	} else {
		s.state = "preview"
		s.lastError = ""
		s.broadcastStateLocked()
	}
}

func (s *serverState) signalDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func rejectUnexpectedRequestBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBodyBytes)
	defer r.Body.Close()

	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return true
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return true
	}
	if n > 0 {
		http.Error(w, "request body not allowed", http.StatusBadRequest)
		return true
	}
	return false
}

func (s *serverState) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(indexHTML); err != nil {
		return
	}
}

func (s *serverState) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	resp := buildStatus(snapshot)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

var htmlRe = regexp.MustCompile(`<[a-zA-Z][\s\S]*>`)

func looksLikeHtml(s string) bool {
	return htmlRe.MatchString(s)
}

func defaultSendCmd(ctx context.Context, rec Recipient, baseDir string) (string, error) {
	args := []string{"gmail", "+send", "--to", rec.Address, "--subject", rec.Subject, "--body", rec.Body}
	if looksLikeHtml(rec.Body) {
		args = append(args, "--html")
	}
	for _, a := range rec.Attachments {
		args = append(args, "-a", resolveRelPath(baseDir, a))
	}
	cmd := exec.CommandContext(ctx, "gws", args...)
	if gwsEnv != nil {
		cmd.Env = gwsEnv
	}
	return runCommandCombinedLimited(cmd, maxCommandOutputBytes)
}

func dryrunSendCmd(_ context.Context, rec Recipient, baseDir string) (string, error) {
	args := []string{"gws", "gmail", "+send", "--to", rec.Address, "--subject", rec.Subject, "--body", rec.Body}
	if looksLikeHtml(rec.Body) {
		args = append(args, "--html")
	}
	for _, a := range rec.Attachments {
		args = append(args, "-a", resolveRelPath(baseDir, a))
	}
	fmt.Printf("[dryrun] %s\n", strings.Join(args, " "))
	return "", nil
}

func (s *serverState) handleSend(w http.ResponseWriter, r *http.Request) {
	if rejectUnexpectedRequestBody(w, r) {
		return
	}
	s.mu.Lock()
	recIdx, rec, err := s.requireActionableStateLocked()
	if err != nil {
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if rec.Status == "Sent" || (rec.Row >= 0 && rec.Row < len(s.app.Rows) && s.app.StatusCol >= 0 && s.app.StatusCol < len(s.app.Rows[rec.Row]) && s.app.Rows[rec.Row][s.app.StatusCol] == "Sent") {
		s.mu.Unlock()
		http.Error(w, "current recipient already sent; skip to continue", http.StatusConflict)
		return
	}
	baseDir := s.app.BaseDir
	sendFn := s.sendCmdFunc
	if sendFn == nil {
		sendFn = defaultSendCmd
	}
	s.state = "sending"
	s.lastError = ""
	s.broadcastStateLocked()
	s.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)

	go func(rec Recipient, recIdx int) {
		ctx, cancel := context.WithTimeout(s.ctx, sendCommandTimeout)
		defer cancel()
		output, err := sendFn(ctx, rec, baseDir)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("command timed out after %s", sendCommandTimeout)
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		if err != nil {
			s.setErrorStateLocked(formatCommandError(err, output))
			return
		}

		if rec.Row < 0 || rec.Row >= len(s.app.Rows) || s.app.StatusCol < 0 || s.app.StatusCol >= len(s.app.Rows[rec.Row]) {
			s.setErrorStateLocked("email sent but failed to update in-memory status")
			return
		}

		s.app.Rows[rec.Row][s.app.StatusCol] = "Sent"
		s.app.Recipients[recIdx].Status = "Sent"
		s.sentRun++
		s.sentEver++
		if err := s.safeWriteCSV(); err != nil {
			s.setErrorStateLocked(fmt.Sprintf("email sent but failed to update CSV: %v\nUse Skip to continue without resending.", err))
			return
		}

		s.state = "sent"
		s.broadcastStateLocked()

		time.AfterFunc(1500*time.Millisecond, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.state == "sent" {
				s.advance()
			}
		})
	}(rec, recIdx)
}

func (s *serverState) handleSkip(w http.ResponseWriter, r *http.Request) {
	if rejectUnexpectedRequestBody(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recIdx, rec, err := s.requireActionableStateLocked()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if rec.Status == "Sent" {
		s.advance()
		w.WriteHeader(http.StatusOK)
		return
	}
	if rec.Row < 0 || rec.Row >= len(s.app.Rows) || s.app.StatusCol < 0 || s.app.StatusCol >= len(s.app.Rows[rec.Row]) {
		s.setErrorStateLocked("failed to update in-memory status")
		http.Error(w, "failed to update in-memory status", http.StatusInternalServerError)
		return
	}
	s.app.Rows[rec.Row][s.app.StatusCol] = "Skipped"
	s.app.Recipients[recIdx].Status = "Skipped"
	if err := s.safeWriteCSV(); err != nil {
		s.setErrorStateLocked(fmt.Sprintf("failed to update CSV: %v", err))
		http.Error(w, "failed to update CSV", http.StatusInternalServerError)
		return
	}
	s.skippedRun++
	s.advance()
	w.WriteHeader(http.StatusOK)
}

func (s *serverState) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 16)
	s.mu.Lock()
	s.clients = append(s.clients, ch)
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	data, err := json.Marshal(buildStatus(snapshot))
	if err != nil {
		s.mu.Lock()
		s.removeClientLocked(ch)
		s.mu.Unlock()
		http.Error(w, "failed to encode state", http.StatusInternalServerError)
		return
	}
	defer func() {
		s.mu.Lock()
		s.removeClientLocked(ch)
		s.mu.Unlock()
	}()

	if !writeSSEMessage(w, flusher, string(data)) {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case msg := <-ch:
			if !writeSSEMessage(w, flusher, msg) {
				return
			}
		}
	}
}

func runServer(app *AppData, pending []int, sentEver int, dryrun bool) Summary {
	ctx, cancel := context.WithCancel(context.Background())
	s := &serverState{
		app:      app,
		pending:  pending,
		cursor:   0,
		state:    "preview",
		sentEver: sentEver,
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		dryrun:   dryrun,
	}
	if dryrun {
		s.sendCmdFunc = dryrunSendCmd
	}

	s.csvMon = newCSVMonitor(app.CSVPath)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.csvMon.check() {
					msg := "The CSV file has been modified from external source. In order to protect the file records, this application exited itself purposely."
					fmt.Fprintf(os.Stderr, "\n%s\n", msg)
					s.mu.Lock()
					s.state = "terminated"
					s.lastError = msg
					s.broadcastStateLocked()
					s.mu.Unlock()
					time.Sleep(500 * time.Millisecond)
					os.Exit(1)
				}
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/send", s.handleSend)
	mux.HandleFunc("POST /api/skip", s.handleSkip)
	mux.HandleFunc("GET /api/attachment", s.handleAttachment)
	mux.HandleFunc("GET /api/preview", s.handlePreview)
	mux.HandleFunc("GET /events", s.handleEvents)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot start server: %v\n", err)
		return Summary{SentRun: s.sentRun, SkippedRun: s.skippedRun, SentEver: s.sentEver}
	}

	server := &http.Server{Handler: mux}
	addr := "http://" + ln.Addr().String()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	serveErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
	}()

	fmt.Printf("Listening on %s\n", addr)
	go openBrowser(addr)

	select {
	case <-sigCh:
	case <-s.done:
	case err := <-serveErrCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: server stopped unexpectedly: %v\n", err)
		}
	}

	signal.Stop(sigCh)
	s.cancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: shutting down server: %v\n", err)
	}

	s.mu.Lock()
	summary := Summary{SentRun: s.sentRun, SkippedRun: s.skippedRun, SentEver: s.sentEver}
	s.mu.Unlock()
	return summary
}
