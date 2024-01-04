package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type resMsg struct {
	statusCode int
	body       string
}

type errMsg struct{ err error }

type model struct {
	status       string
	body         string
	uriInput     textinput.Model
	jsonViewport viewport.Model
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			m.status = "fetching"
			return m, fetchPost(m.uriInput.Value())
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			lines := strings.Split(m.jsonViewport.View(), "\n")
			headerHeight := 4
			lineIdx := msg.Y - headerHeight
			if lineIdx < len(lines) {
				line := strings.TrimSpace(lines[lineIdx])
				re := regexp.MustCompile(`"(https://.*)"`)
				matches := re.FindStringSubmatch(line)
				if len(matches) > 1 {
					uri := matches[1]
					m.status = uri
					return m, fetchPost(uri)
				} else {
					m.status = line
				}
			}

			m.status = fmt.Sprintf("%s", msg.String())
		}
	case resMsg:
		m.status = fmt.Sprintf("fetched: %d", msg.statusCode)
		m.body = msg.body
		m.jsonViewport.SetContent(msg.body)
	case errMsg:
		m.status = "error"
	}

	m.uriInput, cmd = m.uriInput.Update(msg)
	cmds = append(cmds, cmd)

	m.jsonViewport, cmd = m.jsonViewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	s := fmt.Sprintf("Status: %s\n\n", m.status)

	s += m.uriInput.View()

	s += fmt.Sprintf("\n\n%s", m.jsonViewport.View())

	return s
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Enter post URL"
	ti.Focus()
	ti.CharLimit = 1024
	ti.Width = 80

	vp := viewport.New(144, 32)

	return model{
		status:       "init",
		uriInput:     ti,
		jsonViewport: vp,
	}
}

func fetchPost(uri string) tea.Cmd {
	return func() tea.Msg {
		c := &http.Client{Timeout: 3 * time.Second}

		req, err := http.NewRequest("GET", uri, nil)
		if err != nil {
			return errMsg{err}
		}

		req.Header.Add("Accept", "application/activity+json")

		res, err := c.Do(req)
		if err != nil {
			return errMsg{err}
		}

		body, err := io.ReadAll(res.Body)
		if err != nil {
			return errMsg{err}
		}

		var indentBuf bytes.Buffer

		json.Indent(&indentBuf, body, "", "  ")

		indentStr := indentBuf.String()

		var highlightBuf strings.Builder

		quick.Highlight(&highlightBuf, indentStr, "json", "terminal256", "monokai")

		return resMsg{
			statusCode: res.StatusCode,
			body:       highlightBuf.String(),
		}
	}
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
