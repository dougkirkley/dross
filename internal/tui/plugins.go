package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dougkirkley/dross/internal/plugins"
)

// editorDoneMsg is delivered after the external editor launched by /config exits.
type editorDoneMsg struct{ err error }

// editConfig opens the config file in $EDITOR (suspending the TUI), creating an
// empty one if it doesn't exist yet.
func (m *Model) editConfig() tea.Cmd {
	path := m.configPath
	if path == "" {
		m.append(errStyle.Render("no config path configured"))
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte("{\n}\n"), 0o600)
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	m.append(metaStyle.Render("opening " + path + " in " + editor + " …"))
	return tea.ExecProcess(exec.Command(editor, path), func(err error) tea.Msg {
		return editorDoneMsg{err}
	})
}

// pluginOpMsg carries the result of an async /plugin operation (git clone /
// install) back to the update loop.
type pluginOpMsg struct {
	text   string
	reload bool
}

// showSkills lists the discovered skills.
func (m *Model) showSkills() {
	if len(m.skillItems) == 0 {
		m.append(metaStyle.Render("no skills — add some with ") + crystalStyle.Render("/plugin marketplace add <repo>") + metaStyle.Render(" then ") + crystalStyle.Render("/plugin install <name>"))
		return
	}
	m.append(agentLabel.Render("Skills"))
	for _, s := range m.skillItems {
		mark := checkStyle.Render("● ")
		invoke := ""
		if s.UserInvocable {
			invoke = metaStyle.Render("  /" + s.Name)
		}
		m.append("  " + mark + crystalStyle.Render(s.Name) + metaStyle.Render("  ["+s.Source+"]"+invoke))
		if s.Desc != "" {
			m.append(metaStyle.Render("      " + truncate(s.Desc, 72)))
		}
	}
}

// pluginTabs are the /plugin modal's tabs.
var pluginTabs = []string{"Installed", "Marketplaces", "Browse"}

// updatePluginView handles keys for the /plugin browser modal.
func (m *Model) updatePluginView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.pluginViewOpen = false
		return m, nil
	case "tab", "right", "l":
		m.pluginTab = (m.pluginTab + 1) % len(pluginTabs)
		m.pluginSel = 0
		return m, nil
	case "shift+tab", "left", "h":
		m.pluginTab = (m.pluginTab + len(pluginTabs) - 1) % len(pluginTabs)
		m.pluginSel = 0
		return m, nil
	case "up", "k":
		if m.pluginSel > 0 {
			m.pluginSel--
		}
		return m, nil
	case "down", "j":
		if m.pluginSel < m.pluginRowCount()-1 {
			m.pluginSel++
		}
		return m, nil
	case "g", "home":
		m.pluginSel = 0
		return m, nil
	case "G", "end":
		m.pluginSel = m.pluginRowCount() - 1
		return m, nil
	case " ", "enter":
		return m, m.pluginAction()
	case "x":
		return m, m.pluginRemove()
	}
	return m, nil
}

func (m Model) pluginRowCount() int {
	st, err := plugins.LoadState()
	if err != nil {
		return 0
	}
	switch m.pluginTab {
	case 0:
		return len(st.Plugins)
	case 1:
		return len(st.Marketplaces)
	default:
		av, _ := plugins.Available()
		return len(av)
	}
}

// pluginAction runs space/enter on the selected row: toggle enable (Installed),
// or install (Browse).
func (m *Model) pluginAction() tea.Cmd {
	st, err := plugins.LoadState()
	if err != nil {
		m.append(errStyle.Render(err.Error()))
		return nil
	}
	switch m.pluginTab {
	case 0: // toggle enable
		if m.pluginSel < len(st.Plugins) {
			p := st.Plugins[m.pluginSel]
			return m.pluginSync(fmt.Sprintf("%s %s", toggleWord(!p.Enabled), p.Name),
				func() error { return plugins.SetEnabled(p.Name, !p.Enabled) })
		}
	case 2: // install
		av, _ := plugins.Available()
		if m.pluginSel < len(av) {
			l := av[m.pluginSel]
			if l.Installed {
				m.append(metaStyle.Render(l.Name + " already installed"))
				return nil
			}
			ref := l.Name + "@" + l.Marketplace
			m.append(metaStyle.Render("installing " + ref + " …"))
			return func() tea.Msg {
				p, err := plugins.Install(ref)
				if err != nil {
					return pluginOpMsg{text: "install failed: " + err.Error()}
				}
				return pluginOpMsg{text: "installed " + p.Name, reload: true}
			}
		}
	}
	return nil
}

// pluginRemove runs 'x': uninstall (Installed) or remove marketplace (Marketplaces).
func (m *Model) pluginRemove() tea.Cmd {
	st, err := plugins.LoadState()
	if err != nil {
		m.append(errStyle.Render(err.Error()))
		return nil
	}
	switch m.pluginTab {
	case 0:
		if m.pluginSel < len(st.Plugins) {
			n := st.Plugins[m.pluginSel].Name
			return m.pluginSync("uninstalled "+n, func() error { return plugins.Uninstall(n) })
		}
	case 1:
		if m.pluginSel < len(st.Marketplaces) {
			n := st.Marketplaces[m.pluginSel].Name
			return m.pluginSync("removed marketplace "+n, func() error { return plugins.RemoveMarketplace(n) })
		}
	}
	return nil
}

func toggleWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// pluginBox renders the /plugin browser modal (tabs + list for the active tab).
func (m Model) pluginBox(bodyH int) string {
	boxW := 78
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 44 {
		boxW = 44
	}
	viol := lipgloss.NewStyle().Foreground(holoViol)

	// Tab bar.
	var tabs []string
	for i, t := range pluginTabs {
		if i == m.pluginTab {
			tabs = append(tabs, viol.Bold(true).Render(t))
		} else {
			tabs = append(tabs, metaStyle.Render(t))
		}
	}
	header := viol.Bold(true).Render("Plugins  ") + strings.Join(tabs, metaStyle.Render("  ·  "))

	st, _ := plugins.LoadState()
	var lines []string
	switch m.pluginTab {
	case 0:
		if st == nil || len(st.Plugins) == 0 {
			lines = append(lines, metaStyle.Render("  no plugins installed — see the Browse tab"))
		}
		for i, p := range st.Plugins {
			state := doneGlyph.Render("● ")
			if !p.Enabled {
				state = metaStyle.Render("○ ")
			}
			lines = append(lines, m.pluginRow(i, state+crystalStyle.Render(p.Name)+metaStyle.Render("  "+p.Marketplace)))
		}
	case 1:
		if st == nil || len(st.Marketplaces) == 0 {
			lines = append(lines, metaStyle.Render("  none — /plugin marketplace add <owner/repo | git-url>"))
		}
		for i, mp := range st.Marketplaces {
			lines = append(lines, m.pluginRow(i, crystalStyle.Render(mp.Name)+metaStyle.Render("  "+mp.Source)))
		}
	default:
		av, _ := plugins.Available()
		if len(av) == 0 {
			lines = append(lines, metaStyle.Render("  no marketplaces added — /plugin marketplace add <owner/repo>"))
		}
		for i, l := range av {
			mark := crystalStyle.Render("+ ")
			if l.Installed {
				mark = doneGlyph.Render("✓ ")
			}
			row := mark + crystalStyle.Render(l.Name) + metaStyle.Render("  "+l.Marketplace)
			if l.Description != "" {
				row += metaStyle.Render(" · " + truncate(l.Description, 40))
			}
			lines = append(lines, m.pluginRow(i, row))
		}
	}

	foot := metaStyle.Render("tab switch · ↑/↓ select · space toggle/install · x remove · esc close · add via /plugin marketplace add")
	content := lipgloss.JoinVertical(lipgloss.Left, header, "", strings.Join(lines, "\n"), "", foot)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoViol).
		Padding(0, 2).Width(boxW).Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) pluginRow(i int, s string) string {
	if i == m.pluginSel {
		return lipgloss.NewStyle().Foreground(holoViol).Render("› ") + s
	}
	return "  " + s
}

// skillsBox renders the /skills modal: each skill with its on/off state, source,
// and rough token cost; plugin skills are locked (managed via /plugin).
func (m Model) skillsBox(bodyH int) string {
	boxW := 72
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 40 {
		boxW = 40
	}
	viol := lipgloss.NewStyle().Foreground(holoViol)
	title := viol.Bold(true).Render("Skills")
	sub := metaStyle.Render(fmt.Sprintf("%d skills · space to toggle · enter to save · esc to close", len(m.skillItems)))

	var lines []string
	for i, s := range m.skillItems {
		locked := s.Source != "project" && s.Source != "user"
		state := doneGlyph.Render("● on ")
		if m.skillOff[s.Name] {
			state = metaStyle.Render("○ off")
		} else if !s.UserInvocable {
			state = checkStyle.Render("◆ auto")
		}
		note := metaStyle.Render(" · " + s.Source)
		if locked {
			note += metaStyle.Render(" · locked")
		}
		tok := metaStyle.Render(fmt.Sprintf(" · ~%d tok", (len(s.Body)+3)/4))
		row := state + "  " + crystalStyle.Render(s.Name) + note + tok
		if i == m.skillSel {
			row = viol.Render("› ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}

	body := strings.Join(lines, "\n")
	content := lipgloss.JoinVertical(lipgloss.Left, title, sub, "", body, "",
		metaStyle.Render("plugin skills are managed via /plugin"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoViol).
		Padding(0, 2).Width(boxW).Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

// handlePlugin runs a "/plugin ..." command. Network operations (marketplace
// add, install) run async and refresh the skill list on completion.
func (m *Model) handlePlugin(text string) tea.Cmd {
	m.input.Reset()
	args := strings.Fields(text)[1:]
	if len(args) == 0 {
		m.pluginViewOpen, m.pluginTab, m.pluginSel = true, 0, 0
		return nil
	}
	switch args[0] {
	case "marketplace", "mp":
		return m.handleMarketplace(args[1:])
	case "install":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin install <name[@marketplace]>"))
			return nil
		}
		ref := args[1]
		m.append(metaStyle.Render("installing " + ref + " …"))
		return func() tea.Msg {
			p, err := plugins.Install(ref)
			if err != nil {
				return pluginOpMsg{text: "install failed: " + err.Error()}
			}
			return pluginOpMsg{text: fmt.Sprintf("installed %s (from %s)", p.Name, p.Marketplace), reload: true}
		}
	case "uninstall":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin uninstall <name>"))
			return nil
		}
		return m.pluginSync("uninstalled "+args[1], func() error { return plugins.Uninstall(args[1]) })
	case "enable":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin enable <name>"))
			return nil
		}
		return m.pluginSync("enabled "+args[1], func() error { return plugins.SetEnabled(args[1], true) })
	case "disable":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin disable <name>"))
			return nil
		}
		return m.pluginSync("disabled "+args[1], func() error { return plugins.SetEnabled(args[1], false) })
	case "list":
		m.showPluginStatus()
		return nil
	default:
		m.append(errStyle.Render("unknown: /plugin " + args[0] + " — try: marketplace add, install, uninstall, enable, disable, list"))
		return nil
	}
}

func (m *Model) handleMarketplace(args []string) tea.Cmd {
	if len(args) == 0 {
		m.showPluginStatus()
		return nil
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin marketplace add <owner/repo | git-url | path>"))
			return nil
		}
		src := args[1]
		m.append(metaStyle.Render("adding marketplace " + src + " …"))
		return func() tea.Msg {
			mp, err := plugins.AddMarketplace(src)
			if err != nil {
				return pluginOpMsg{text: "marketplace add failed: " + err.Error()}
			}
			return pluginOpMsg{text: fmt.Sprintf("added marketplace %q (%d plugins) — /plugin install <name>", mp.Name, len(mp.Plugins)), reload: true}
		}
	case "remove":
		if len(args) < 2 {
			m.append(errStyle.Render("usage: /plugin marketplace remove <name>"))
			return nil
		}
		return m.pluginSync("removed marketplace "+args[1], func() error { return plugins.RemoveMarketplace(args[1]) })
	case "list":
		m.showPluginStatus()
		return nil
	default:
		m.append(errStyle.Render("unknown: /plugin marketplace " + args[0]))
		return nil
	}
}

// pluginSync runs a fast (non-network) plugin op and reloads the skill list.
func (m *Model) pluginSync(okMsg string, fn func() error) tea.Cmd {
	if err := fn(); err != nil {
		m.append(errStyle.Render(err.Error()))
		return nil
	}
	m.append(metaStyle.Render(okMsg))
	m.applyReload()
	return nil
}

// showPluginStatus lists added marketplaces and installed plugins.
func (m *Model) showPluginStatus() {
	st, err := plugins.LoadState()
	if err != nil {
		m.append(errStyle.Render(err.Error()))
		return
	}
	m.append(agentLabel.Render("Marketplaces"))
	if len(st.Marketplaces) == 0 {
		m.append(metaStyle.Render("  none · /plugin marketplace add <owner/repo>"))
	}
	for _, mp := range st.Marketplaces {
		m.append("  " + crystalStyle.Render(mp.Name) + metaStyle.Render("  "+mp.Source))
	}
	m.append(agentLabel.Render("Plugins"))
	if len(st.Plugins) == 0 {
		m.append(metaStyle.Render("  none · /plugin install <name>"))
	}
	for _, p := range st.Plugins {
		state := checkStyle.Render("● ")
		if !p.Enabled {
			state = metaStyle.Render("○ ")
		}
		m.append("  " + state + crystalStyle.Render(p.Name) + metaStyle.Render("  "+p.Marketplace))
	}
}
