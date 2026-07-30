// Package claudecode is a provider that drives the locally-installed Claude Code
// CLI (`claude`) in headless mode. It uses whatever auth the CLI already has —
// typically a Claude subscription — so dross can talk to Claude without an
// Anthropic API key. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/claudecode"
//
// Two modes:
//
//   - Driven (when the session has dross tools): Claude Code's built-in tools
//     are disabled and its persona is replaced with a system prompt that lists
//     dross's tools and a strict XML call format. Claude only reasons and
//     emits tool calls; dross parses and executes them. dross's own agent
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

	"github.com/dougkirkley/dross/pkg/provider"
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
		// mode, so auto-accept edits (bypass fully with DROSS_CLAUDE_DANGEROUS).
		s.permission = "acceptEdits"
		if v := os.Getenv("DROSS_CLAUDE_DANGEROUS"); v == "1" || v == "true" {
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

// buildSystem produces the driven-mode system prompt: dross's own system,
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
	b.WriteString("- Use ONLY the tools under \"Available tools\". Their names are lowercase EXACTLY as shown (e.g. `read`, `bash` — never `Read`, `Grep`, or any tool not listed).\n")
	b.WriteString("- Tool results come back as <tool_result> messages. NEVER emit <tool_result>, <function_results>, or repeat tool output back — the results are given to you; use them, don't reproduce them.\n")
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

	// The driven model (esp. opus) often emits its NATIVE tool XML —
	// <invoke name="read"><parameter name="path">… — instead of the
	// <function=read><parameter=path>… form the prompt asks for. Normalize the
	// attribute form to the "=name" form so paramRE/funcRE capture the args;
	// otherwise values (e.g. a file path) arrive empty and tool calls corrupt.
	invokeOpenRE = regexp.MustCompile(`<invoke\s+name="([^"]+)"\s*>`)
	invokeCloseRE = regexp.MustCompile(`</invoke>`)
	funcNameRE   = regexp.MustCompile(`<function\s+name="([^"]+)"\s*>`)
	paramNameRE  = regexp.MustCompile(`<parameter\s+name="([^"]+)"\s*>`)
	// resultBlockRE removes echoed result blocks (the model sometimes parrots the
	// <tool_result>/<function_results> wrappers it was handed).
	resultBlockRE = regexp.MustCompile(`(?s)<(tool_result|tool_error|function_results|function_calls)\b[^>]*>.*?</(tool_result|tool_error|function_results|function_calls)>`)
	// strayTagRE removes any leftover protocol tags (malformed/half-emitted).
	strayTagRE = regexp.MustCompile(`</?(function|parameter|tool_result|tool_error|function_results|function_calls|invoke|system-reminder)\b[^>]*>`)
	// reminderRE removes <system-reminder> blocks the driven Claude Code CLI
	// injects (e.g. "this tool call took a long time"); they are harness noise,
	// not the model's reply, so they must not surface as assistant text.
	reminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
)

// toolAlias maps common Claude-native tool names to dross's lowercase tools, so
// a driven model that reaches for its built-in names still hits the right tool.
var toolAlias = map[string]string{
	"read": "read", "write": "write", "edit": "edit", "bash": "bash",
	"grep": "grep", "glob": "glob", "webfetch": "web_fetch", "web_fetch": "web_fetch",
}

// normalizeToolName lowercases and de-aliases a tool name from the model.
func normalizeToolName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if a, ok := toolAlias[n]; ok {
		return a
	}
	return n
}

// parseCalls extracts XML tool calls from the model's text and returns them plus
// the text with those blocks removed.
// normalizeProtocol rewrites the model's native tool XML (invoke/name=" ") into
// the <function=…>/<parameter=…> form the parser expects.
func normalizeProtocol(s string) string {
	s = invokeOpenRE.ReplaceAllString(s, "<function=$1>")
	s = invokeCloseRE.ReplaceAllString(s, "</function>")
	s = funcNameRE.ReplaceAllString(s, "<function=$1>")
	s = paramNameRE.ReplaceAllString(s, "<parameter=$1>")
	return s
}

func parseCalls(s string) ([]provider.ToolCall, string) {
	s = normalizeProtocol(s)
	if !strings.Contains(s, "<function=") {
		return nil, stripProtocol(s)
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
			Name:  normalizeToolName(m[1]),
			Input: raw,
		})
	}
	return calls, stripProtocol(funcRE.ReplaceAllString(s, ""))
}

// stripProtocol removes tool-call protocol scaffolding (echoed result blocks and
// stray tags) so it never leaks into the user-facing assistant text.
func stripProtocol(s string) string {
	s = reminderRE.ReplaceAllString(s, "")
	s = resultBlockRE.ReplaceAllString(s, "")
	s = strayTagRE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// formatResults renders dross tool results as a user message Claude continues
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
		// emits our XML tool calls for dross to execute.
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
		calls, cleaned := parseCalls(out)
		if len(calls) > 0 {
			return &provider.Response{Text: cleaned, ToolCalls: calls, StopReason: provider.StopToolUse, Usage: usage}, nil
		}
		return &provider.Response{Text: cleaned, StopReason: provider.StopEndTurn, Usage: usage}, nil
	}
	return &provider.Response{Text: out, StopReason: provider.StopEndTurn, Usage: usage}, nil
}
