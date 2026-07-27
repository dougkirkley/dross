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
		{Kind: "tool", Data: `bash {"command":"go build ./... && go test ./..."}`},
		{Kind: "tool-result", Data: "ok  github.com/dougkirkley/holocron  0.4s"},
		{Kind: "text", Data: "Done. --json flag wired; boilerplate delegated to the local model. Build and tests pass."},
		{Kind: "checkpoint", Data: "saved handoff brief"},
	}
	for _, e := range events {
		m.applyEvent(e)
	}
	m.cost = 0.0142
	m.turns = 6
	return m.View()
}
