// Package claudecode is a provider that drives the locally-installed Claude Code
// CLI (`claude`) in headless mode. It uses whatever auth the CLI already has —
// typically a Claude subscription — so ergon can talk to Claude without an
// Anthropic API key. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/ergon/internal/provider/claudecode"
//
// Two modes:
//
//   - Driven (when the session has ergon tools): Claude Code's built-in tools
//     are disabled and its persona is replaced with a system prompt that lists
//     ergon's tools and a strict XML call format. Claude only reasons and
//     emits tool calls; ergon parses and executes them. ergon's own agent
//     loop (delegate, background shells, reviewer, sandbox) stays in control.
//   - Thin (no tools): the prompt is forwarded and Claude Code runs its own loop.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/dougkirkley/ergon/internal/provider"
)

func init() {
	provider.Register("claude-code", func(cfg provider.Config) (provider.Provider, error) {
		bin := "claude"
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("claude CLI not found on PATH (install Claude Code)")
		}
		return &prov{bin: bin}, nil
	})
}

type prov struct{ bin string }

func (p *prov) Name() string { return "claude-code" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	model := cfg.Model
	if model == "" {
		model = "sonnet"
	}
	s := &session{prov: p, model: model, tools: cfg.Tools}
	if len(cfg.Tools) > 0 {
		s.driven = true
		s.system = buildSystem(cfg.System, cfg.Tools)
	} else {
		// Thin mode: Claude Code owns permissioning but can't prompt in headless
		// mode, so auto-accept edits (bypass fully with ERGON_CLAUDE_DANGEROUS).
		s.permission = "acceptEdits"
		if v := os.Getenv("ERGON_CLAUDE_DANGEROUS"); v == "1" || v == "true" {
			s.permission = ""
		}
	}
	return s
}

type session struct {
	prov       *prov
	model      string
	tools      []provider.ToolDef
	driven     bool
	system     string
	permission string
	sessionID  string // captured from the first turn, reused via --resume
}

// buildSystem produces the driven-mode system prompt: ergon's own system,
// then the tool-call protocol and the tool catalog.
func buildSystem(base string, tools []provider.ToolDef) string {
	var b strings.Builder
	if strings.TrimSpace(base) != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("# Tool protocol\n")
	b.WriteString("You are a function-calling engine. You have NO built-in tools of your own. ")
	b.WriteString("You DO have the tools listed under \"Available tools\" below — never claim a tool is unavailable or that tool access is broken; it is not.\n\n")
	b.WriteString("The only way to act is to emit calls in EXACTLY this form:\n\n")
	b.WriteString("<function=TOOL_NAME>\n<parameter=PARAM_NAME>VALUE</parameter>\n</function>\n\n")
	b.WriteString("Example — to read a file:\n<function=read>\n<parameter=path>go.mod</parameter>\n</function>\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- To act, output one or more <function> blocks and NOTHING else — no prose.\n")
	b.WriteString("- Put each parameter's value verbatim between its tags (multi-line is fine).\n")
	b.WriteString("- Never describe a call in words; emit the block.\n")
	b.WriteString("- Tool results come back as <tool_result> messages; continue from there.\n")
	b.WriteString("- Only when the task is fully complete, reply with your final answer as plain text and NO <function> blocks.\n\n")
	b.WriteString("# Available tools\n")
	for _, t := range tools {
		b.WriteString("\n## " + t.Name + "\n")
		if t.Description != "" {
			b.WriteString(t.Description + "\n")
		}
		names := make([]string, 0, len(t.Properties))
		for name := range t.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		required := map[string]bool{}
		for _, r := range t.Required {
			required[r] = true
		}
		for _, name := range names {
			desc := ""
			if m, ok := t.Properties[name].(map[string]any); ok {
				if d, ok := m["description"].(string); ok {
					desc = " — " + d
				}
			}
			req := ""
			if required[name] {
				req = " (required)"
			}
			b.WriteString("- " + name + req + desc + "\n")
		}
	}
	return b.String()
}

var (
	funcRE  = regexp.MustCompile(`(?s)<function=([^>\s]+)\s*>(.*?)</function>`)
	paramRE = regexp.MustCompile(`(?s)<parameter=([^>\s]+)\s*>(.*?)</parameter>`)
)

// parseCalls extracts XML tool calls from the model's text and returns them plus
// the text with those blocks removed.
func parseCalls(s string) ([]provider.ToolCall, string) {
	if !strings.Contains(s, "<function=") {
		return nil, s
	}
	var calls []provider.ToolCall
	for i, m := range funcRE.FindAllStringSubmatch(s, -1) {
		args := map[string]string{}
		for _, p := range paramRE.FindAllStringSubmatch(m[2], -1) {
			args[strings.TrimSpace(p[1])] = strings.TrimSpace(p[2])
		}
		raw, err := json.Marshal(args)
		if err != nil {
			continue
		}
		calls = append(calls, provider.ToolCall{
			ID:    fmt.Sprintf("call_%d", i),
			Name:  strings.TrimSpace(m[1]),
			Input: raw,
		})
	}
	cleaned := strings.TrimSpace(funcRE.ReplaceAllString(s, ""))
	return calls, cleaned
}

// formatResults renders ergon tool results as a user message Claude continues
// from.
func formatResults(rs []provider.ToolResult) string {
	var b strings.Builder
	for _, r := range rs {
		tag := "tool_result"
		if r.IsError {
			tag = "tool_error"
		}
		b.WriteString("<" + tag + ">\n" + r.Content + "\n</" + tag + ">\n")
	}
	return b.String()
}

// streamEvent is the subset of Claude Code's stream-json we read.
type streamEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Usage   struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	content := strings.TrimSpace(in.Text)
	if len(in.ToolResults) > 0 {
		content = strings.TrimSpace(formatResults(in.ToolResults))
	}
	if content == "" {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	args := []string{"-p", content, "--output-format", "stream-json", "--verbose", "--model", s.model}
	if s.sessionID != "" {
		args = append(args, "--resume", s.sessionID)
	} else if s.driven {
		// First turn: install our system prompt and strip Claude Code's persona.
		args = append(args, "--system-prompt", s.system, "--exclude-dynamic-system-prompt-sections")
	}
	if s.driven {
		// Give Claude Code ZERO tools (--tools with an empty list) and strip
		// global MCP servers/settings, so it can't act on its own and instead
		// emits our XML tool calls for ergon to execute.
		args = append(args, "--tools", "", "--strict-mcp-config", "--setting-sources", "")
	} else if s.permission == "" {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args, "--permission-mode", s.permission)
	}

	cmd := exec.CommandContext(ctx, s.prov.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var final, assistant strings.Builder
	var usage provider.Usage
	isErr := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // tool outputs can be large
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e streamEvent
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "system":
			if e.SessionID != "" {
				s.sessionID = e.SessionID // reused via --resume next turn
			}
		case "assistant":
			for _, b := range e.Message.Content {
				if b.Type == "text" {
					assistant.WriteString(b.Text)
				}
			}
			usage.InputTokens += e.Message.Usage.InputTokens
			usage.OutputTokens += e.Message.Usage.OutputTokens
		case "result":
			final.WriteString(e.Result)
			isErr = e.IsError
			if e.Usage.InputTokens > 0 || e.Usage.OutputTokens > 0 {
				usage.InputTokens, usage.OutputTokens = e.Usage.InputTokens, e.Usage.OutputTokens
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	out := final.String()
	if strings.TrimSpace(out) == "" {
		out = assistant.String()
	}
	if isErr {
		return nil, fmt.Errorf("claude: %s", strings.TrimSpace(out))
	}

	if s.driven {
		if calls, cleaned := parseCalls(out); len(calls) > 0 {
			return &provider.Response{Text: cleaned, ToolCalls: calls, StopReason: provider.StopToolUse, Usage: usage}, nil
		}
	}
	return &provider.Response{Text: out, StopReason: provider.StopEndTurn, Usage: usage}, nil
}
