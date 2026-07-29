package tui

import (
	"encoding/json"
	"time"

	"github.com/dougkirkley/ergon/pkg/agent"
)

// jsonString returns s as a JSON-quoted string, for building demo tool args.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// DemoWelcomeView renders the empty-state splash (big logo) for screenshots.
// variant selects the logo style (see renderLogo).
func DemoWelcomeView(width, height, variant int) string {
	m := New("ergon", "claude-opus-5", "anthropic", nil)
	m.width, m.height = width, height
	m.logoVariant = 1
	m.welcomeStyle = variant // vary the empty-state layout for iterations
	m.shimmer = demoShimmer  // phase for screenshots of the animated shimmer
	m.SetMeta("v0.4.0", "/Users/doug/Projects/holocron")
	m.SetWorker(func(string, string) {}, "ollama", "qwen3-coder:30b")
	m.layout()
	return m.View()
}

// demoShimmer sets the welcome shimmer phase for --render screenshots.
var demoShimmer int

// SetDemoShimmer sets the shimmer phase used by DemoWelcomeView (for capturing
// animation frames).
func SetDemoShimmer(n int) { demoShimmer = n }

// DemoView renders a populated TUI at the given size for screenshots and manual
// inspection — no live model required.
func DemoView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()

	m.append(youLabel.Render("you") + "  add a --json flag to the CLI and delegate the boilerplate")

	events := []agent.Event{
		{Kind: "text", Data: "Plan: add the flag, delegate the struct + marshalling to the worker, then wire and verify."},
		{Kind: "tool", Data: `read {"path":"cmd/ergon/main.go"}`},
		{Kind: "tool-result", Data: "1  package main ... (142 lines)"},
		{Kind: "tool", Data: `delegate {"task":"In internal/cli/output.go add an Output struct with JSON tags and a Render(w) method..."}`},
		{Kind: "worker:text", Data: "Added Output struct with json tags and Render()."},
		{Kind: "worker:tool", Data: `write {"path":"internal/cli/output.go"}`},
		{Kind: "worker:tool-result", Data: "wrote 38 lines to internal/cli/output.go"},
		{Kind: "tool-result", Data: "worker: added Output struct; internal/cli/output.go compiles."},
		{Kind: "tool", Data: `edit {"path":"cmd/ergon/main.go","old_str":"flag.Parse()","new_str":"jsonOut := fs.Bool(\"json\", false, ...)"}`},
		{Kind: "edit", Data: `{"file":"cmd/ergon/main.go","added":6,"removed":1}`},
		{Kind: "ask", Data: "Name the flag --json or --output=json?"},
		{Kind: "answer", Data: "--json"},
		{Kind: "tool", Data: `bash {"command":"go build ./... && go test ./..."}`},
		{Kind: "tool-result", Data: "ok  github.com/dougkirkley/ergon  0.4s"},
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

// DemoBangView renders the shell-escape state: a "!command" in the input, with
// the gold border/mark/hint that mark shell mode.
func DemoBangView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(promptMark.Render("! ") + youLabel.Render("bash") + "  git status -s")
	m.applyEvent(agent.Event{Kind: "tool-result", Data: " M internal/tui/tui.go\n?? internal/tui/bang_test.go"})
	m.input.SetValue("!go build ./...")
	return m.View()
}

// DemoProvidersView renders the /model picker: providers as subtitles with their
// models underneath.
func DemoProvidersView(width, height int) string {
	m := New("ergon", "qwen3-coder:30b", "ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.provider = "ollama"
	m.openModelPicker([]ModelGroup{
		{Provider: "anthropic", Models: []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}},
		{Provider: "deepseek", Models: []string{"deepseek-chat", "deepseek-reasoner"}},
		{Provider: "groq", Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}},
		{Provider: "openai", Models: []string{"gpt-5", "gpt-5-mini", "o4-mini"}},
		{Provider: "xai", Models: []string{"grok-4", "grok-3-mini"}},
	})
	m.modelSel = 5
	return m.View()
}

// DemoModelsView renders the /model picker: served models grouped by provider.
func DemoModelsView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic", nil)
	m.width, m.height = width, height
	m.SetWorker(func(string, string) {}, "ollama", "qwen3-coder:30b") // enable the Lead/Worker toggle
	m.layout()
	m.openModelPicker([]ModelGroup{
		{Provider: "anthropic", Models: []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"}},
		{Provider: "ollama", Models: []string{"qwen3-coder:30b", "qwen2.5-coder:14b", "deepseek-coder-v2:16b"}},
		{Provider: "groq", Models: []string{"llama-3.3-70b", "kimi-k2"}},
	})
	return m.View()
}

// DemoPluginView renders the /plugin browser modal (reads live ~/.ergon state).
func DemoPluginView(width, height, tab int) string {
	m := New("ergon", "claude-opus-5", "anthropic", nil)
	m.width, m.height = width, height
	m.layout()
	m.pluginViewOpen, m.pluginTab = true, tab
	return m.View()
}

// DemoSkillsView renders the /skills modal for screenshots.
func DemoSkillsView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic", nil)
	m.width, m.height = width, height
	m.skillOff = map[string]bool{"regression-lens": true}
	m.SetSkillItems([]SkillItem{
		{Name: "review-mr", Desc: "Review a merge request", Source: "user", Body: makeBody(1200), UserInvocable: true},
		{Name: "regression-lens", Desc: "Find regressions in a diff", Source: "project", Body: makeBody(640), UserInvocable: true},
		{Name: "docs:grill-with-docs", Desc: "Answer using project docs", Source: "docs", Body: makeBody(160), UserInvocable: false},
		{Name: "flow:ticket", Desc: "Create a ticket from context", Source: "flow", Body: makeBody(360), UserInvocable: true},
	})
	m.layout()
	m.skillsViewOpen, m.skillSel = true, 1
	return m.View()
}

func makeBody(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// DemoToolsView renders the /tools expandable viewer with one entry expanded to
// its full command + output.
func DemoToolsView(width, height int) string {
	m := New("ergon", "claude-opus-5", "ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.toolRecs = []toolRec{
		{name: "read", args: "cmd/ergon/main.go", result: "1  package main\n2\n3  import \"github.com/dougkirkley/ergon/internal/cli\""},
		{name: "bash", args: "go build ./... && go test ./...",
			result: "ok  github.com/dougkirkley/ergon/pkg/agent  0.42s\nok  github.com/dougkirkley/ergon/internal/tui  0.19s\nok  github.com/dougkirkley/ergon/internal/cli  0.31s\nok  github.com/dougkirkley/ergon/internal/config  0.44s"},
		{name: "edit", args: "internal/cli/output.go", result: "replaced 1 occurrence"},
	}
	m.toolSel = 1
	m.toolViewOpen = true
	return m.View()
}

// DemoUsageView renders the /usage output: a context-fill bar per session and
// model, for the provider in use.
func DemoUsageView(width, height int) string {
	m := New("ergon", "qwen3-coder:30b", "ollama", nil)
	m.width, m.height = width, height
	m.layout()
	// Sessions across a few models (one with no known context max), plus the
	// live current one — aggregated per model.
	m.sessions = []session{
		{title: "add --json flag", model: "qwen2.5-coder:14b", turns: 4, inTok: 9200, outTok: 1400},
		{title: "refactor auth", model: "qwen3-coder:30b", turns: 9, inTok: 26800, outTok: 5100},
		{title: "tune prompts", model: "mistral-custom", turns: 3, inTok: 4100, outTok: 900},
		{}, // current
	}
	m.cur = 3
	m.title, m.turns, m.inTok, m.outTok, m.cost = "wire the login flow", 6, 18420, 3240, 0.0
	m.handleSlash("/usage")
	return m.View()
}

// DemoSlashView renders slash-command autocomplete: matches listed above the
// input as the user types "/".
func DemoSlashView(width, height int) string {
	m := New("ergon", "qwen3-coder:30b", "ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  wire up the login flow")
	m.input.SetValue("/r")
	return m.View()
}

// DemoRunningView renders a mid-run frame — spinner, a rotating status phrase,
// and the live token counter — for screenshots of the working state.
func DemoRunningView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  add a --json flag to the CLI and delegate the boilerplate")
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Plan: add the flag, delegate the boilerplate, then wire and verify."},
		{Kind: "tool", Data: `read {"path":"cmd/ergon/main.go"}`},
		{Kind: "edit", Data: `{"file":"internal/cli/output.go","added":38,"removed":0}`},
		{Kind: "usage", Data: `{"in":12800,"out":640}`},
	} {
		m.applyEvent(e)
	}
	m.running = true
	m.phraseIdx = 2 // "Decrypting the ergon"
	m.tick = 40     // ~8s elapsed at 5 fps
	m.cost = 0.0091
	return m.View()
}

// DemoSubagentsView renders a mid-run frame with the live subagent tree, where
// the lead has fanned several subtasks out to parallel workers.
func DemoSubagentsView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
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
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
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
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  reconcile flux and check the live selectors")
	cmd := "cd /workspace/k8s-homelab\n" +
		"for k in infra-core infra-controllers ionic-system; do\n" +
		"kubectl annotate kustomization -n flux-system $k reconcile.fluxcd.io/requestedAt=\"$(date -u +%Y-%m)\"; done"
	for _, e := range []agent.Event{
		{Kind: "text", Data: "Kicking a tail in the background, then reconciling flux."},
		{Kind: "tool", Data: `bash {"command":"kubectl logs -f deploy/ergon-web -n media","background":true}`},
		{Kind: "shell:start", Data: "sh_1\x1fkubectl logs -f deploy/ergon-web -n media"},
		{Kind: "usage", Data: `{"in":18200,"out":859}`},
		// A foreground bash currently executing (drives the "Running N shell
		// commands…" block); no tool-result yet.
		{Kind: "tool", Data: `bash {"command":` + jsonString(cmd) + `}`},
	} {
		m.applyEvent(e)
	}
	if s := m.shells["sh_1"]; s != nil {
		s.start = s.start.Add(-3 * time.Minute)
	}
	m.bashStart = m.bashStart.Add(-17 * time.Second)
	m.running = true
	m.phraseIdx = 6
	m.tick = 95 * 5
	m.cost = 0.0206
	return m.View()
}

// DemoHelpView renders the shortcuts overlay for screenshots.
func DemoHelpView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
	m.width, m.height = width, height
	m.layout()
	m.helpOpen = true
	return m.View()
}

// DemoSessionsView renders the session switcher overlay for screenshots.
func DemoSessionsView(width, height int) string {
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
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
	m := New("ergon", "claude-opus-5", "anthropic · ollama", nil)
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
