package tui

import "github.com/dougkirkley/holocron/internal/agent"

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

// DemoAskView renders the multi-question ask_user picker for screenshots.
func DemoAskView(width, height int) string {
	m := New("holocron", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  review the branch before I merge")
	m.pendingAsk = &askState{
		questions: []agent.AskQuestion{
			{Header: "Scope", Question: "How should I scope the review?", Options: []string{
				"Post-merge commits only", "Whole MR, 367 files", "Risky judgment calls only"}},
			{Header: "Reviewer", Question: "Which model reviews?", Options: []string{
				"A different, stronger model", "Same model"}},
		},
		sel: make([]int, 2),
		tab: 0,
	}
	return m.View()
}
