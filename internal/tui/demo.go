package tui

import (
	"time"

	"github.com/dougkirkley/holocron/internal/agent"
)

// DemoWelcomeView renders the empty-state splash (big logo) for screenshots.
// variant selects the logo style (see renderLogo).
func DemoWelcomeView(width, height, variant int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.logoVariant = variant
	m.layout()
	return m.View()
}

// DemoView renders a populated TUI at the given size for screenshots and manual
// inspection — no live model required.
func DemoView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()

	m.append(youLabel.Render("you") + "  add a --json flag to the CLI and delegate the boilerplate")

	events := []agent.Event{
		{Kind: "text", Data: "Plan: add the flag, delegate the struct + marshalling to the worker, then wire and verify."},
		{Kind: "tool", Data: `read {"path":"cmd/holocron/main.go"}`},
		{Kind: "tool-result", Data: "1  package main ... (142 lines)"},
		{Kind: "tool", Data: `delegate {"task":"In internal/cli/output.go add an Output struct with JSON tags and a Render(w) method..."}`},
		{Kind: "worker:text", Data: "Added Output struct with json tags and Render()."},
		{Kind: "worker:tool", Data: `write {"path":"internal/cli/output.go"}`},
		{Kind: "worker:tool-result", Data: "wrote 38 lines to internal/cli/output.go"},
		{Kind: "tool-result", Data: "worker: added Output struct; internal/cli/output.go compiles."},
		{Kind: "tool", Data: `edit {"path":"cmd/holocron/main.go","old_str":"flag.Parse()","new_str":"jsonOut := fs.Bool(\"json\", false, ...)"}`},
		{Kind: "edit", Data: `{"file":"cmd/holocron/main.go","added":6,"removed":1}`},
		{Kind: "ask", Data: "Name the flag --json or --output=json?"},
		{Kind: "answer", Data: "--json"},
		{Kind: "tool", Data: `bash {"command":"go build ./... && go test ./..."}`},
		{Kind: "tool-result", Data: "ok  github.com/dougkirkley/holocron  0.4s"},
		{Kind: "text", Data: "Done. --json flag wired; boilerplate delegated to the local model. Build and tests pass."},
		{Kind: "usage", Data: `{"in":18420,"out":1240}`},
		{Kind: "review", Data: "LGTM — the flag defaults to false and Render() handles the empty case."},
		{Kind: "checkpoint", Data: "saved handoff brief"},
	}
	for _, e := range events {
		m.applyEvent(e)
	}
	m.cost = 0.0142
	m.turns = 6
	return m.View()
}

// DemoRunningView renders a mid-run frame — spinner, a rotating status phrase,
// and the live token counter — for screenshots of the working state.
func DemoRunningView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  add a --json flag to the CLI and delegate the boilerplate")
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Plan: add the flag, delegate the boilerplate, then wire and verify."},
		{Kind: "tool", Data: `read {"path":"cmd/holocron/main.go"}`},
		{Kind: "edit", Data: `{"file":"internal/cli/output.go","added":38,"removed":0}`},
		{Kind: "usage", Data: `{"in":12800,"out":640}`},
	} {
		m.applyEvent(e)
	}
	m.running = true
	m.phraseIdx = 2 // "Decrypting the holocron"
	m.tick = 40     // ~8s elapsed at 5 fps
	m.cost = 0.0091
	return m.View()
}

// DemoSubagentsView renders a mid-run frame with the live subagent tree, where
// the lead has fanned several subtasks out to parallel workers.
func DemoSubagentsView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  migrate the three helm charts to the new values schema")
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Plan: fan the three charts out to workers, then reconcile and verify."},
		{Kind: "tool", Data: `delegate {"tasks":["migrate application chart","migrate ingress chart","migrate monitoring chart"]}`},
		{Kind: "text", Data: "delegating 3 subtasks (max 2 parallel)"},
		{Kind: "worker:tool", Data: "migrate application chart\x1f" + `read {"path":"charts/application/application_helm.go"}`},
		{Kind: "worker:tool", Data: "migrate ingress chart\x1f" + `read {"path":"charts/ingress/values.yaml"}`},
		{Kind: "worker:edit", Data: "migrate application chart\x1f" + `{"file":"charts/application/values.yaml","added":12,"removed":4}`},
		{Kind: "worker:tool", Data: "migrate ingress chart\x1f" + `bash {"command":"helm lint charts/ingress"}`},
		{Kind: "usage", Data: `{"in":22400,"out":1580}`},
	} {
		m.applyEvent(e)
	}
	m.running = true
	m.phraseIdx = 4
	m.tick = 60 // ~12s
	m.cost = 0.0206
	return m.View()
}

// DemoSubagentFocusView renders the detail overlay for a selected subagent —
// what arrow-selecting a worker and pressing enter shows.
func DemoSubagentFocusView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  migrate the three helm charts to the new values schema")
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Plan: fan the three charts out to workers, then reconcile and verify."},
		{Kind: "tool", Data: `delegate {"tasks":["migrate application chart","migrate ingress chart"]}`},
		{Kind: "worker:tool", Data: "migrate application chart\x1f" + `read {"path":"charts/application/application_helm.go"}`},
		{Kind: "worker:tool-result", Data: "migrate application chart\x1f142 lines"},
		{Kind: "worker:edit", Data: "migrate application chart\x1f" + `{"file":"charts/application/values.yaml","added":12,"removed":4}`},
		{Kind: "worker:tool", Data: "migrate application chart\x1f" + `bash {"command":"helm lint charts/application"}`},
		{Kind: "worker:tool", Data: "migrate ingress chart\x1f" + `read {"path":"charts/ingress/values.yaml"}`},
	} {
		m.applyEvent(e)
	}
	m.running = true
	m.phraseIdx = 4
	m.tick = 70
	m.cost = 0.0231
	m.workerSel = 1
	m.workerFocus = "migrate application chart" // detail of the first subagent open
	return m.View()
}

// DemoShellsView renders a mid-run frame with background shells: one still
// running, one exited, plus the "· N shells" footer count.
func DemoShellsView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  bring up the dev server and run the watcher")
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Starting the dev server and a test watcher in the background."},
		{Kind: "tool", Data: `bash {"command":"npm run dev","background":true}`},
		{Kind: "shell:start", Data: "sh_1\x1fnpm run dev"},
		{Kind: "shell:output", Data: "sh_1\x1fVITE ready on http://localhost:5173\n"},
		{Kind: "tool", Data: `bash {"command":"go test ./... -watch","background":true}`},
		{Kind: "shell:start", Data: "sh_2\x1fgo test ./... -watch"},
		{Kind: "shell:exit", Data: "sh_2\x1fexited 0"},
		{Kind: "usage", Data: `{"in":14200,"out":720}`},
	} {
		m.applyEvent(e)
	}
	// Backdate the running shell so it shows a realistic runtime.
	if s := m.shells["sh_1"]; s != nil {
		s.start = m.shells["sh_1"].start.Add(-3 * time.Minute)
	}
	m.running = true
	m.phraseIdx = 6
	m.tick = 45
	m.cost = 0.0113
	return m.View()
}

// DemoHelpView renders the shortcuts overlay for screenshots.
func DemoHelpView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.helpOpen = true
	return m.View()
}

// DemoSessionsView renders the session switcher overlay for screenshots.
func DemoSessionsView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.sessions = []session{
		{title: "add --json flag to the CLI", cost: 0.0142, lines: []string{"x"},
			summary: "wired --json + Render()", updated: time.Now().Add(-35 * time.Second)},
		{title: "refactor the config loader", cost: 0.0031, lines: []string{"x"},
			summary: "extracted Load() + env", updated: time.Now().Add(-8 * time.Minute)},
		{title: "debug the SSE reconnect", cost: 0.0088, lines: []string{"x"},
			summary: "repro'd the drop on ?after", updated: time.Now().Add(-13 * time.Hour)},
	}
	m.cur = 0
	m.running = true // session 0 shows under "Working"
	m.pickerOpen = true
	m.pickOrder = m.buildPickOrder()
	m.pickIdx = 0
	return m.View()
}

// DemoAskView renders the multi-question ask_user picker for screenshots.
func DemoAskView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  review the branch before I merge")
	qs := []agent.AskQuestion{
		{Header: "Scope", Question: "How should I scope the review?", Options: []string{
			"Post-merge commits only", "Whole MR, 367 files", "Risky judgment calls only"}},
		{Header: "Lenses", Question: "Which review lenses?", MultiSelect: true, Options: []string{
			"Correctness", "Security", "Performance"}},
	}
	st := &askState{
		questions: qs,
		sel:       make([][]bool, len(qs)),
		cursor:    make([]int, len(qs)),
		tab:       1, // show the multi-select question
	}
	for i, q := range qs {
		st.sel[i] = make([]bool, len(q.Options))
	}
	st.sel[0][0] = true // Scope answered (shows ✓ on its tab)
	st.sel[1][0] = true // two lenses chosen
	st.sel[1][1] = true
	m.pendingAsk = st
	return m.View()
}
