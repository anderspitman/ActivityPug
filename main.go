package main

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	history      []string
	rootUri      string
	privKey      *rsa.PrivateKey
	logFile      *os.File
}

func (m *model) Init() tea.Cmd {
	return nil
}

const HeaderHeight = 5

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			m.status = "fetching"
			return m, m.navigateTo(m.uriInput.Value())
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {

			if msg.X >= 0 && msg.X <= 8 && msg.Y >= 0 && msg.Y <= 2 {
				//m.status = fmt.Sprintf("x: %d, y: %d", msg.X, msg.Y)
				m.status = "back button clicked"
				return m, m.navigateBack()
			}

			lines := strings.Split(m.body, "\n")
			lineIdx := msg.Y - HeaderHeight + m.jsonViewport.YOffset
			if lineIdx < len(lines) {
				line := strings.TrimSpace(lines[lineIdx])
				// TODO: don't recompile regex every time
				re := regexp.MustCompile(`"(https://.*)"`)
				matches := re.FindStringSubmatch(line)
				if len(matches) > 1 {
					formatted := fmt.Sprintf("\"%s\"", matches[1])
					uri, err := strconv.Unquote(formatted)
					if err != nil {
						m.status = "Error: " + err.Error()
						return m, nil
					}

					m.status = uri
					return m, m.navigateTo(uri)
				} else {
					m.status = line
				}
			}
		}
	case tea.WindowSizeMsg:
		m.jsonViewport.Width = msg.Width - 2
		m.jsonViewport.Height = msg.Height - HeaderHeight - 2
		m.status = "size"
	case resMsg:
		m.status = fmt.Sprintf("fetched: %d", msg.statusCode)
		m.body = msg.body

		var highlightBuf strings.Builder
		quick.Highlight(&highlightBuf, msg.body, "json", "terminal256", "monokai")

		m.jsonViewport.SetContent(highlightBuf.String())
	case errMsg:
		m.status = "error"
	}

	m.uriInput, cmd = m.uriInput.Update(msg)
	cmds = append(cmds, cmd)

	m.jsonViewport, cmd = m.jsonViewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	s := ""

	backBtnStyle := lipgloss.NewStyle().
		Width(8).
		Align(lipgloss.Center).
		BorderStyle(lipgloss.NormalBorder())

	s += backBtnStyle.Render("Back")

	s += "\n"

	s += m.uriInput.View()

	codeStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		Width(m.jsonViewport.Width)

	s += fmt.Sprintf("\n%s", codeStyle.Render(m.jsonViewport.View()))

	s += fmt.Sprintf("\nStatus: %s", m.status)

	return s
}

func newModel(rootUri string, privKey *rsa.PrivateKey, logFile *os.File) *model {
	ti := textinput.New()
	ti.Placeholder = "Enter URL"
	ti.Focus()
	ti.CharLimit = 1024
	ti.Width = 80

	vp := viewport.New(80, 32)

	return &model{
		status:       "init",
		uriInput:     ti,
		jsonViewport: vp,
		history:      []string{},
		rootUri:      rootUri,
		privKey:      privKey,
		logFile:      logFile,
	}
}

func (m *model) navigateTo(uri string) tea.Cmd {

	lastIdx := len(m.history) - 1
	if lastIdx >= 0 {
		curUri := m.history[lastIdx]
		if curUri == uri {
			return nil
		}
	}

	m.history = append(m.history, uri)
	m.uriInput.SetValue(uri)
	return m.fetchPost(uri)
}

func (m *model) navigateBack() tea.Cmd {
	lastIdx := len(m.history) - 1
	if lastIdx < 1 {
		return nil
	}
	prevUri := m.history[lastIdx-1]
	m.history = m.history[:lastIdx]
	m.uriInput.SetValue(prevUri)
	return m.fetchPost(prevUri)
}

func (m *model) fetchPost(uri string) tea.Cmd {
	return func() tea.Msg {
		c := &http.Client{Timeout: 3 * time.Second}

		req, err := http.NewRequest("GET", uri, nil)
		if err != nil {
			return errMsg{err}
		}

		parsedUrl, err := url.Parse(uri)
		dateHeader := time.Now().UTC().Format(http.TimeFormat)

		req.Header.Set("Accept", "application/activity+json")
		req.Header.Set("Date", dateHeader)
		req.Header.Set("Host", parsedUrl.Host)

		pubKeyId := fmt.Sprintf("%s#main-key", m.rootUri)

		err = sign(m.privKey, pubKeyId, req)
		if err != nil {
			return errMsg{err}
		}

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

		return resMsg{
			statusCode: res.StatusCode,
			//body:       highlightBuf.String(),
			body: indentStr,
		}
	}
}

func main() {

	rootUriArg := flag.String("root-uri", "", "Root URI")
	preferredUsernameArg := flag.String("preferred-username", "", "Preferred username")
	nameArg := flag.String("name", "", "Name")
	flag.Parse()

	rootUri := *rootUriArg
	preferredUsername := *preferredUsernameArg
	name := *nameArg

	privPemPath := filepath.Join("./", "private_key.pem")
	_, err := os.Stat(privPemPath)
	if err != nil {
		privKey, err := MakeRSAKey()
		if err != nil {
			log.Fatal(err)
		}

		err = SaveRSAKey(privPemPath, privKey)
		if err != nil {
			log.Fatal(err)
		}

	}

	privKey, err := LoadRSAKey(privPemPath)
	if err != nil {
		log.Fatal(err)
	}

	publicKeyPem, err := GetPublicKeyPem(privKey)
	if err != nil {
		log.Fatal(err)
	}

	actor := &Actor{
		Context:           []string{"https://www.w3.org/ns/activitystreams"},
		Type:              "Person",
		Id:                rootUri,
		PreferredUsername: preferredUsername,
		Name:              name,
		Inbox:             fmt.Sprintf("%s/inbox", rootUri),
		Outbox:            fmt.Sprintf("%s/outbox", rootUri),
		Followers:         fmt.Sprintf("%s/followers", rootUri),
		PublicKey: &PublicKey{
			Id:           fmt.Sprintf("%s#main-key", rootUri),
			Owner:        rootUri,
			PublicKeyPem: publicKeyPem,
		},
	}

	logFile, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		fmt.Fprintf(logFile, "%s\n", r.URL.Path)

		w.Header().Set("Content-Type", "application/activity+json")
		json.NewEncoder(w).Encode(actor)
	})

	go func() {
		http.ListenAndServe(":9004", nil)
	}()

	p := tea.NewProgram(newModel(rootUri, privKey, logFile), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
