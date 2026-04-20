package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/ledongthuc/pdf"
)

type state int

const (
	statePreview state = iota
	stateSending
	stateSent
	stateError
)

type sendResultMsg struct{ err error }
type autoAdvanceMsg struct{}

type model struct {
	app        *AppData
	pending    []int
	cursor     int
	state      state
	err        error
	sentRun    int
	sentEver   int
	width      int
	height     int
	viewport   viewport.Model
	pdfText    string
	pdfPreview bool
}

func extractPDFText(path string) string {
	if !strings.HasSuffix(strings.ToLower(path), ".pdf") {
		return ""
	}
	f, r, err := pdf.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader, err := r.GetPlainText()
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	buf.ReadFrom(reader)
	return buf.String()
}

func loadPDFPreview(m *model) {
	r := m.app.Recipients[m.pending[m.cursor]]
	text := extractPDFText(r.Attach)
	m.pdfText = text
	m.pdfPreview = text != ""
	if m.pdfPreview {
		m.viewport.SetContent(text)
	}
	m.viewport.GotoTop()
}

func newModel(app *AppData, pending []int, sentEver int) model {
	m := model{
		app:      app,
		pending:  pending,
		cursor:   0,
		state:    statePreview,
		sentEver: sentEver,
		width:    80,
		height:   24,
		viewport: viewport.New(viewport.WithWidth(76), viewport.WithHeight(10)),
	}
	loadPDFPreview(&m)
	return m
}

func (m model) currentRecipient() Recipient {
	return m.app.Recipients[m.pending[m.cursor]]
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(m.boxWidth() - 6)
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()
		switch m.state {
		case statePreview, stateError:
			switch key {
			case "enter":
				m.state = stateSending
				return m, m.execSendCmd()
			case "esc":
				return m, tea.Quit
			case "ctrl+c":
				return m, tea.Quit
			default:
				if m.pdfPreview {
					var cmd tea.Cmd
					m.viewport, cmd = m.viewport.Update(msg)
					return m, cmd
				}
			}
		}

	case sendResultMsg:
		if msg.err != nil {
			m.state = stateError
			m.err = msg.err
			return m, nil
		}
		r := m.currentRecipient()
		m.app.Rows[r.Row][m.app.StatusCol] = "Sent"
		if err := saveCSV(m.app.CSVPath, m.app.Headers, m.app.Rows); err != nil {
			m.state = stateError
			m.err = fmt.Errorf("email sent but failed to update CSV: %w", err)
			return m, nil
		}
		m.sentRun++
		m.sentEver++
		m.state = stateSent
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return autoAdvanceMsg{}
		})

	case autoAdvanceMsg:
		m.cursor++
		if m.cursor >= len(m.pending) {
			return m, tea.Quit
		}
		m.state = statePreview
		loadPDFPreview(&m)
		return m, nil
	}
	return m, nil
}

func (m model) execSendCmd() tea.Cmd {
	r := m.currentRecipient()
	parts := strings.Fields(r.Command)
	return func() tea.Msg {
		cmd := exec.Command(parts[0], parts[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return sendResultMsg{fmt.Errorf("%w\n%s", err, string(output))}
		}
		return sendResultMsg{nil}
	}
}

func (m model) View() tea.View {
	var s string
	if m.cursor >= len(m.pending) {
		s = ""
	} else {
		switch m.state {
		case statePreview:
			s = m.renderPreview("")
		case stateSending:
			s = m.renderPreview(sendingStyle.Render("  Sending..."))
		case stateSent:
			s = m.renderPreview(sentStyle.Render("  Sent Successfully"))
		case stateError:
			s = m.renderError()
		}
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

var (
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 2)

	promptStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("241")).
			Padding(0, 2)

	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	sendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	sentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	barFilled    = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	barEmpty     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

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

func (m model) boxWidth() int {
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	return w
}

func renderProgressBar(current, total, width int) string {
	if total == 0 {
		return ""
	}
	barWidth := width - 8
	if barWidth < 10 {
		barWidth = 10
	}
	filled := barWidth * current / total
	return fmt.Sprintf("  %s%s %d%%",
		barFilled.Render(strings.Repeat("█", filled)),
		barEmpty.Render(strings.Repeat("░", barWidth-filled)),
		current*100/total,
	)
}

func (m model) renderBoxes(r Recipient, w int) string {
	innerW := w - 6 // border (1) + padding (2) on each side
	if innerW < 1 {
		innerW = 1
	}
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("─", innerW))

	content := fmt.Sprintf(
		"%s  %s\n%s  %s\n%s\n%s  %s (%s)\n%s\n%s",
		labelStyle.Render("To:      "), valueStyle.Render(r.Address),
		labelStyle.Render("Subject: "), valueStyle.Render(r.Subject),
		sep,
		labelStyle.Render("Attachment:"), valueStyle.Render(r.Attach), fileSize(r.Attach),
		sep,
		r.Body,
	)

	return boxStyle.Width(w).Render(content)
}

func (m model) renderPreview(statusLine string) string {
	r := m.currentRecipient()
	w := m.boxWidth()
	total := len(m.app.Recipients)

	boxes := m.renderBoxes(r, w)
	progress := fmt.Sprintf("  Email %d of %d pending  |  %d/%d sent overall", m.cursor+1, len(m.pending), m.sentEver, total)
	bar := renderProgressBar(m.sentEver, total, w)
	prompt := promptStyle.Width(w).Render(helpStyle.Render("Press Enter to send the email immediately  |  Press ESC to cancel"))

	parts := []string{boxes, "", progress, bar, "", prompt}
	if statusLine != "" {
		parts = append(parts, statusLine)
	}

	return strings.Join(parts, "\n")
}

func (m model) renderError() string {
	r := m.currentRecipient()
	w := m.boxWidth()
	total := len(m.app.Recipients)

	boxes := m.renderBoxes(r, w)
	progress := fmt.Sprintf("  Email %d of %d pending  |  %d/%d sent overall", m.cursor+1, len(m.pending), m.sentEver, total)
	bar := renderProgressBar(m.sentEver, total, w)
	errMsg := errorStyle.Render(fmt.Sprintf("  Error: %v", m.err))
	prompt := promptStyle.Width(w).Render(helpStyle.Render("Press Enter to retry sending  |  Press ESC to abort"))

	return strings.Join([]string{boxes, "", progress, bar, "", prompt, errMsg}, "\n")
}
