package main

import (
	"archive/zip"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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
	mu        sync.Mutex
	app       *AppData
	pending   []int
	cursor    int
	state     string
	lastError string
	sentRun    int
	skippedRun int
	sentEver   int
	clients   []chan string
	done      chan struct{}
	doneOnce  sync.Once
}

type statusResponse struct {
	State     string            `json:"state"`
	Recipient *recipientJSON    `json:"recipient,omitempty"`
	Progress  progressJSON      `json:"progress"`
	Error     string            `json:"error"`
}

type recipientJSON struct {
	Address    string `json:"address"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Attach     string `json:"attach"`
	AttachSize string `json:"attachSize"`
	AttachExt  string `json:"attachExt"`
}

type progressJSON struct {
	Current    int `json:"current"`
	Pending    int `json:"pending"`
	SentRun    int `json:"sentRun"`
	SkippedRun int `json:"skippedRun"`
	SentEver   int `json:"sentEver"`
	Total      int `json:"total"`
}

func extractPreviewText(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		out, err := exec.Command("pdftotext", path, "-").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	case ".txt":
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	case ".docx":
		return extractDocxText(path)
	}
	return ""
}

func extractDocxText(path string) string {
	r, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		return parseDocxXML(rc)
	}
	return ""
}

func parseDocxXML(r io.Reader) string {
	dec := xml.NewDecoder(r)
	var paragraphs []string
	var current strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			} else if t.Name.Local == "p" {
				paragraphs = append(paragraphs, current.String())
				current.Reset()
			}
		case xml.CharData:
			if inT {
				current.Write(t)
			}
		}
	}
	return strings.TrimSpace(strings.Join(paragraphs, "\n"))
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
	cmd.Start()
}

func (s *serverState) currentRecipient() Recipient {
	return s.app.Recipients[s.pending[s.cursor]]
}

func (s *serverState) buildStatus() statusResponse {
	resp := statusResponse{
		State: s.state,
		Progress: progressJSON{
			Current:    s.cursor + 1,
			Pending:    len(s.pending),
			SentRun:    s.sentRun,
			SkippedRun: s.skippedRun,
			SentEver:   s.sentEver,
			Total:      len(s.app.Recipients),
		},
		Error: s.lastError,
	}
	if s.state != "done" && s.cursor < len(s.pending) {
		r := s.currentRecipient()
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(r.Attach)), ".")
		resp.Recipient = &recipientJSON{
			Address:    r.Address,
			Subject:    r.Subject,
			Body:       r.Body,
			Attach:     r.Attach,
			AttachSize: fileSize(r.Attach),
			AttachExt:  ext,
		}
	}
	return resp
}

func (s *serverState) broadcastState() {
	data, err := json.Marshal(s.buildStatus())
	if err != nil {
		return
	}
	msg := string(data)
	var active []chan string
	for _, ch := range s.clients {
		select {
		case ch <- msg:
			active = append(active, ch)
		default:
		}
	}
	s.clients = active
}

func (s *serverState) advance() {
	s.cursor++
	if s.cursor >= len(s.pending) {
		s.state = "done"
		s.broadcastState()
		time.AfterFunc(2*time.Second, func() {
			s.signalDone()
		})
	} else {
		s.state = "preview"
		s.lastError = ""
		s.broadcastState()
	}
}

func (s *serverState) signalDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *serverState) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *serverState) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp := s.buildStatus()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *serverState) handleSend(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state != "preview" && s.state != "error" {
		s.mu.Unlock()
		http.Error(w, "not in sendable state", http.StatusConflict)
		return
	}
	s.state = "sending"
	s.lastError = ""
	s.broadcastState()
	rec := s.currentRecipient()
	s.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)

	go func() {
		parts := strings.Fields(rec.Command)
		cmd := exec.Command(parts[0], parts[1:]...)
		output, err := cmd.CombinedOutput()

		s.mu.Lock()
		defer s.mu.Unlock()

		if err != nil {
			s.state = "error"
			s.lastError = fmt.Sprintf("%v\n%s", err, strings.TrimSpace(string(output)))
			s.broadcastState()
			return
		}

		s.app.Rows[rec.Row][s.app.StatusCol] = "Sent"
		if err := saveCSV(s.app.CSVPath, s.app.Headers, s.app.Rows); err != nil {
			s.state = "error"
			s.lastError = fmt.Sprintf("email sent but failed to update CSV: %v", err)
			s.broadcastState()
			return
		}

		s.sentRun++
		s.sentEver++
		s.state = "sent"
		s.broadcastState()

		time.AfterFunc(1500*time.Millisecond, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.state == "sent" {
				s.advance()
			}
		})
	}()
}

func (s *serverState) handleSkip(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "sending" {
		http.Error(w, "cannot skip while sending", http.StatusConflict)
		return
	}
	rec := s.currentRecipient()
	s.app.Rows[rec.Row][s.app.StatusCol] = "Skipped"
	if err := saveCSV(s.app.CSVPath, s.app.Headers, s.app.Rows); err != nil {
		s.state = "error"
		s.lastError = fmt.Sprintf("failed to update CSV: %v", err)
		s.broadcastState()
		return
	}
	s.skippedRun++
	s.advance()
	w.WriteHeader(http.StatusOK)
}

func (s *serverState) handleAttachment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state == "done" || s.cursor >= len(s.pending) {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	path := s.currentRecipient().Attach
	s.mu.Unlock()
	http.ServeFile(w, r, path)
}

func (s *serverState) handlePreview(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state == "done" || s.cursor >= len(s.pending) {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := s.currentRecipient().Attach
	s.mu.Unlock()

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || (ext != ".txt" && ext != ".docx") {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text := extractPreviewText(path)
	if text == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(text))
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
	// Send initial state
	data, _ := json.Marshal(s.buildStatus())
	s.mu.Unlock()

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func runServer(app *AppData, pending []int, sentEver int) Summary {
	s := &serverState{
		app:      app,
		pending:  pending,
		cursor:   0,
		state:    "preview",
		sentEver: sentEver,
		done:     make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/send", s.handleSend)
	mux.HandleFunc("POST /api/skip", s.handleSkip)
	mux.HandleFunc("GET /api/attachment", s.handleAttachment)
	mux.HandleFunc("GET /api/preview", s.handlePreview)
	mux.HandleFunc("GET /events", s.handleEvents)

	ln, err := net.Listen("tcp", "0.0.0.0:8123")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot start server: %v\n", err)
		return Summary{SentRun: s.sentRun, SkippedRun: s.skippedRun, SentEver: s.sentEver}
	}

	server := &http.Server{Handler: mux}
	addr := "http://0.0.0.0:8123"

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	go server.Serve(ln)

	fmt.Printf("Listening on %s\n", addr)
	go openBrowser(addr)

	select {
	case <-sigCh:
	case <-s.done:
	}

	signal.Stop(sigCh)
	ln.Close()
	server.Close()

	return Summary{SentRun: s.sentRun, SkippedRun: s.skippedRun, SentEver: s.sentEver}
}
