package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
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
	viewport    viewport.Model
	hasPreview  bool
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

func loadPreview(m *model) {
	r := m.app.Recipients[m.pending[m.cursor]]
	text := extractPreviewText(r.Attach)
	m.hasPreview = text != ""
	if m.hasPreview {
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
	loadPreview(&m)
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
				if m.hasPreview {
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
		loadPreview(&m)
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

func renderSep(w int) string {
	innerW := w - 6
	if innerW < 1 {
		innerW = 1
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("─", innerW))
}

func (m model) renderBoxes(r Recipient, w int) string {
	sep := renderSep(w)
	content := fmt.Sprintf(
		"%s  %s\n%s  %s\n%s\n%s",
		labelStyle.Render("To:      "), valueStyle.Render(r.Address),
		labelStyle.Render("Subject: "), valueStyle.Render(r.Subject),
		sep,
		r.Body,
	)
	return boxStyle.Width(w).Render(content)
}

func renderAttachLine(r Recipient) string {
	return fmt.Sprintf("%s  %s (%s)", labelStyle.Render("Attachment:"), valueStyle.Render(r.Attach), fileSize(r.Attach))
}

func (m model) renderBottom(promptText string) string {
	w := m.boxWidth()
	total := len(m.app.Recipients)
	progress := fmt.Sprintf("  Email %d of %d pending  |  %d/%d sent overall", m.cursor+1, len(m.pending), m.sentEver, total)
	bar := renderProgressBar(m.sentEver, total, w)
	prompt := promptStyle.Width(w).Render(helpStyle.Render(promptText))
	return strings.Join([]string{"", progress, bar, "", prompt}, "\n")
}

func renderAttachBox(r Recipient, w int) string {
	return boxStyle.Width(w).Render(renderAttachLine(r))
}

func renderPreviewBox(vp viewport.Model, r Recipient, availH, w int) string {
	innerW := w - 6
	sep := renderSep(w)
	attachLine := renderAttachLine(r)
	scrollPct := fmt.Sprintf(" %3.0f%% ", vp.ScrollPercent()*100)
	previewLabel := helpStyle.Render("Preview") + strings.Repeat(" ", max(0, innerW-lipgloss.Width("Preview")-lipgloss.Width(scrollPct))) + helpStyle.Render(scrollPct)

	headerH := lipgloss.Height(attachLine + "\n" + sep + "\n" + previewLabel)
	footerH := lipgloss.Height(sep + "\n" + "x")
	innerH := availH - 4 - headerH - footerH
	if innerH < 1 {
		innerH = 1
	}
	vp.SetHeight(innerH)
	vp.SetWidth(innerW)

	content := attachLine + "\n" + sep + "\n" + previewLabel + "\n" + vp.View() + "\n" + sep + "\n" + helpStyle.Render("↑↓ / PgUp·PgDn to scroll")
	return boxStyle.Width(w).Render(content)
}

func renderStatusLine(statusLine string) string {
	if statusLine != "" {
		return statusLine
	}
	return " "
}

func (m model) renderLayout(promptText, statusLine string) string {
	r := m.currentRecipient()
	w := m.boxWidth()

	top := m.renderBoxes(r, w)
	status := renderStatusLine(statusLine)
	bottom := m.renderBottom(promptText)

	topH := lipgloss.Height(top)
	statusH := lipgloss.Height(status)
	bottomH := lipgloss.Height(bottom)
	availH := m.height - topH - statusH - bottomH

	var middle string
	if m.hasPreview && availH > 4 {
		middle = renderPreviewBox(m.viewport, r, availH, w)
	} else {
		middle = renderAttachBox(r, w)
	}
	middleH := lipgloss.Height(middle)
	if pad := availH - middleH; pad > 0 {
		middle += strings.Repeat("\n", pad)
	}

	return top + "\n" + middle + status + "\n" + bottom
}

func (m model) renderPreview(statusLine string) string {
	return m.renderLayout("Press Enter to send the email immediately  |  Press ESC to cancel", statusLine)
}

func (m model) renderError() string {
	errMsg := errorStyle.Render(fmt.Sprintf("  Error: %v", m.err))
	return m.renderLayout("Press Enter to retry sending  |  Press ESC to abort", errMsg)
}
