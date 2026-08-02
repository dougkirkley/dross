package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dougkirkley/dross/pkg/agent"
)

// MCPServerInfo is one MCP server surfaced in the /mcp modal, with its spec and
// where it came from. Supplied by the host via SetMCPServers.
type MCPServerInfo struct {
	Spec     agent.MCPServerSpec
	Source   string // "config" | "discovered"
	Editable bool   // config servers can be removed; discovered are read-only
}

// mcpRow is a server row with its live probe status.
type mcpRow struct {
	info   MCPServerInfo
	state  string // "probing" | "ok" | "error"
	tools  int
	errMsg string
}

// mcpProbeMsg reports the result of probing one server (by row index).
type mcpProbeMsg struct {
	idx   int
	tools int
	err   error
}

// SetMCPServers wires the /mcp modal's server list (configured + discovered).
func (m *Model) SetMCPServers(fn func() []MCPServerInfo) { m.mcpProvider = fn }

// openMCPView populates the modal from the provider and kicks off a live probe
// of every server. Returns the batched probe commands.
func (m *Model) openMCPView() tea.Cmd {
	var infos []MCPServerInfo
	if m.mcpProvider != nil {
		infos = m.mcpProvider()
	}
	m.mcpRows = make([]mcpRow, len(infos))
	for i, info := range infos {
		m.mcpRows[i] = mcpRow{info: info, state: "probing"}
	}
	m.mcpViewOpen, m.mcpDetail, m.mcpSel = true, false, 0
	cmds := make([]tea.Cmd, 0, len(infos))
	for i, info := range infos {
		cmds = append(cmds, probeMCPCmd(i, info.Spec))
	}
	return tea.Batch(cmds...)
}

// probeMCPCmd connects to a server off the UI thread and reports tool count.
func probeMCPCmd(idx int, spec agent.MCPServerSpec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		n, err := agent.ProbeMCP(ctx, spec)
		return mcpProbeMsg{idx: idx, tools: n, err: err}
	}
}

// applyMCPProbe records a probe result on its row.
func (m *Model) applyMCPProbe(msg mcpProbeMsg) {
	if msg.idx < 0 || msg.idx >= len(m.mcpRows) {
		return
	}
	r := &m.mcpRows[msg.idx]
	if msg.err != nil {
		r.state, r.errMsg = "error", oneLine(msg.err.Error())
		return
	}
	r.state, r.tools = "ok", msg.tools
}

// handleMCPKey routes keys while the /mcp modal is open. Returns true if handled.
func (m *Model) handleMCPKey(key string) bool {
	if !m.mcpViewOpen {
		return false
	}
	if m.mcpDetail {
		switch key {
		case "esc", "q", "enter", "left", "h":
			m.mcpDetail = false
		}
		return true
	}
	switch key {
	case "esc", "q":
		m.mcpViewOpen = false
	case "up", "k":
		if m.mcpSel > 0 {
			m.mcpSel--
		}
	case "down", "j":
		if m.mcpSel < len(m.mcpRows)-1 {
			m.mcpSel++
		}
	case "g", "home":
		m.mcpSel = 0
	case "G", "end":
		if len(m.mcpRows) > 0 {
			m.mcpSel = len(m.mcpRows) - 1
		}
	case "enter", "right", "l":
		if m.mcpSel < len(m.mcpRows) {
			m.mcpDetail = true
		}
	}
	return true
}

// statusCell renders a row's live status glyph + label.
func (r mcpRow) statusCell() string {
	switch r.state {
	case "ok":
		return doneGlyph.Render("● ") + resultStyle.Render(fmt.Sprintf("connected · %d tools", r.tools))
	case "error":
		if isAuthErr(r.errMsg) {
			return goldStyle.Render("△ ") + goldStyle.Render("needs auth")
		}
		return errStyle.Render("△ ") + resultStyle.Render("unavailable")
	default:
		return metaStyle.Render("◌ probing…")
	}
}

func isAuthErr(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "auth")
}

func (m Model) mcpBox(bodyH int) string {
	boxW := 72
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 40 {
		boxW = 40
	}
	viol := lipgloss.NewStyle().Foreground(holoViol)
	title := viol.Bold(true).Render("MCP servers")
	sub := metaStyle.Render(fmt.Sprintf("%d servers · enter for details · esc to close", len(m.mcpRows)))

	var lines []string
	if len(m.mcpRows) == 0 {
		lines = append(lines, metaStyle.Render("no MCP servers · add with: dross mcp add <name> <url>"))
	}
	for i, r := range m.mcpRows {
		name := crystalStyle.Render(r.info.Spec.Name)
		src := metaStyle.Render(" · " + r.info.Source)
		row := r.statusCell() + "  " + name + src
		if i == m.mcpSel {
			row = viol.Render("› ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, sub, "",
		strings.Join(lines, "\n"), "",
		metaStyle.Render("add/remove: dross mcp · /mcp add · config mcp: · .dross/mcp/*.json"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoViol).
		Padding(0, 2).Width(boxW).Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) mcpDetailBox(bodyH int) string {
	boxW := 72
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 40 {
		boxW = 40
	}
	if m.mcpSel >= len(m.mcpRows) {
		return m.mcpBox(bodyH)
	}
	r := m.mcpRows[m.mcpSel]
	s := r.info.Spec
	viol := lipgloss.NewStyle().Foreground(holoViol)
	label := func(k string) string { return metaStyle.Render(fmt.Sprintf("%-10s", k)) }

	transport := s.Transport
	if transport == "" {
		transport = "http"
	}
	loc := s.URL
	if transport == "stdio" {
		loc = strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	}
	rows := []string{
		viol.Bold(true).Render(s.Name + " MCP server"),
		"",
		label("Status") + r.statusCell(),
		label("Transport") + agentText.Render(transport),
		label("Location") + agentText.Render(loc),
		label("Source") + agentText.Render(r.info.Source),
	}
	if r.state == "ok" {
		rows = append(rows, label("Tools")+agentText.Render(fmt.Sprintf("%d", r.tools)))
	}
	if r.state == "error" && r.errMsg != "" {
		rows = append(rows, label("Error")+errStyle.Render(truncate(r.errMsg, boxW-14)))
	}
	rows = append(rows, "", metaStyle.Render("esc to go back"))

	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoViol).
		Padding(0, 2).Width(boxW).Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}
