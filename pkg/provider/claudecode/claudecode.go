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
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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
	sessionID  string // captured from the first turn

	// One `claude` process serves every turn of this session.
	//
	// It used to be one process per turn, rejoining the conversation with
	// --resume. Measured against a persistent one on the same trivial turns:
	// 2.5–3.3s a turn spawning, 1.36–1.63s in-process, and the gap grows with
	// the transcript because --resume reloads it every time. It also threw the
	// prompt cache away each turn; kept open, turn two reads back what turn one
	// wrote.
	once  sync.Once
	mu    sync.Mutex // one turn at a time: the protocol coalesces overlapping ones
	wmu   sync.Mutex // an interrupt is written while a turn is being read
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	errs  *ringBuffer
	stop  context.CancelFunc
	reqs  int   // control_request ids, unique within the session
	dead  error // sticky: once the child is gone every later Send fails fast
}

// errClosed is what a Send after Close reports, rather than reviving the child.
var errClosed = fmt.Errorf("claude: session closed")

// maxLine bounds one JSON event. A tool result can be large, but a line longer
// than this is a stream that has lost its framing, and with a persistent
// process that would corrupt every turn after it rather than just this one.
const maxLine = 32 << 20

// ringBuffer keeps the last n bytes written to it, so a child that dies can say
// why without its whole stderr being held for the life of the session.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	n   int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.n {
		r.buf = r.buf[len(r.buf)-r.n:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
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
	invokeOpenRE  = regexp.MustCompile(`<invoke\s+name="([^"]+)"\s*>`)
	invokeCloseRE = regexp.MustCompile(`</invoke>`)
	funcNameRE    = regexp.MustCompile(`<function\s+name="([^"]+)"\s*>`)
	paramNameRE   = regexp.MustCompile(`<parameter\s+name="([^"]+)"\s*>`)
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

// verbatimParams are the tool arguments whose whitespace is part of the value.
//
// This list exists because the parser used to run strings.TrimSpace over every
// argument, which is right for a path and catastrophic for code. edit's old_str
// has to match the file byte for byte, and almost every edit inside a function
// body begins with indentation — trimming it made the match fail, so the agent
// re-read the file, retried with the same indentation, and failed again. A
// one-line change could burn a whole turn budget. write's content lost its
// leading and trailing newlines the same way, silently, in the file that got
// written.
var verbatimParams = map[string]bool{
	"old_str": true, "new_str": true, "content": true,
}

// wholeFileParams hold the entire contents of a file rather than a fragment of
// one.
//
// They need the opposite guess about the last newline. This protocol cannot
// distinguish "the file ends with a newline" from "the closing tag is on its
// own line" — both are written the same way. For a fragment being matched
// against a file, an extra trailing newline breaks the match, so it is dropped.
// For a whole file, a missing final newline is a file that gofmt rewrites and
// every diff complains about, so it is kept.
var wholeFileParams = map[string]bool{"content": true}

// paramValue recovers a tool argument from between its XML tags.
//
// The newline after the opening tag, and the one before the closing tag, are
// the formatting of the block rather than part of the value, so one of each is
// removed — a value that really does begin with a blank line keeps it. Beyond
// that, an argument whose whitespace matters is returned untouched, and
// everything else is trimmed as before, so an indented <parameter=path> still
// yields a clean path.
func paramValue(name, s string) string {
	switch {
	case strings.HasPrefix(s, "\r\n"):
		s = s[2:]
	case strings.HasPrefix(s, "\n"):
		s = s[1:]
	}
	if !wholeFileParams[name] {
		switch {
		case strings.HasSuffix(s, "\r\n"):
			s = s[:len(s)-2]
		case strings.HasSuffix(s, "\n"):
			s = s[:len(s)-1]
		}
	}
	if verbatimParams[name] {
		return s
	}
	return strings.TrimSpace(s)
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
			name := strings.TrimSpace(p[1])
			args[name] = paramValue(name, p[2])
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
		Usage tokenUsage `json:"usage"`
	} `json:"message"`
	Result  string     `json:"result"`
	IsError bool       `json:"is_error"`
	Usage   tokenUsage `json:"usage"`
	// How long the CLI spent waiting on the API, as opposed to how long dross
	// spent waiting on the CLI. The gap between the two is what this provider
	// costs per turn on top of the model.
	DurationAPIMs int64 `json:"duration_api_ms"`
}

// tokenUsage mirrors the token accounting Claude Code reports. Cache tokens are
// the bulk of a resumed turn's context, so they must be counted for the context
// gauge to reflect the real window fill.
type tokenUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// start brings up the one `claude` process this session talks to, on the first
// Send. The flags that used to be rebuilt every turn — model, system prompt,
// tool exclusions, permission mode — are all start-up flags, so they are set
// once here and hold for the life of the conversation.
func (s *session) start() error {
	s.once.Do(func() {
		args := []string{
			"-p",
			"--input-format", "stream-json",
			"--output-format", "stream-json",
			"--verbose",
			"--model", s.model,
		}
		if s.driven {
			// Our system prompt replaces Claude Code's persona, and it gets ZERO
			// tools (--tools with an empty list) plus no global MCP servers or
			// settings, so it can't act on its own and instead emits our XML tool
			// calls for dross to execute.
			args = append(args,
				"--system-prompt", s.system,
				"--exclude-dynamic-system-prompt-sections",
				"--tools", "", "--strict-mcp-config", "--setting-sources", "")
		} else if s.permission == "" {
			args = append(args, "--dangerously-skip-permissions")
		} else {
			args = append(args, "--permission-mode", s.permission)
		}

		// The child outlives any one turn, so it can't hang off a turn's context:
		// the first cancelled turn would kill the conversation. Turn-scoped
		// cancellation is an interrupt on the wire instead — see readTurn.
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, s.prov.bin, args...)
		errs := &ringBuffer{n: 8 << 10}
		cmd.Stderr = errs
		stdin, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			s.dead = fmt.Errorf("claude: stdin: %w", err)
			return
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			s.dead = fmt.Errorf("claude: stdout: %w", err)
			return
		}
		if err := cmd.Start(); err != nil {
			cancel()
			s.dead = fmt.Errorf("claude: start: %w", err)
			return
		}
		s.cmd, s.stdin, s.errs, s.stop = cmd, stdin, errs, cancel
		s.out = bufio.NewReaderSize(stdout, 64<<10)
	})
	return s.dead
}

// Close ends the conversation and reaps the child. Closing stdin is the polite
// exit; the cancel is there for a CLI that doesn't take the hint.
func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		return nil
	}
	_ = s.stdin.Close()
	s.stop()
	_ = s.cmd.Wait()
	s.stdin, s.stop = nil, nil
	if s.dead == nil {
		s.dead = errClosed
	}
	return nil
}

// die records why the child is unusable. Every later Send fails on it rather
// than blocking on a pipe nobody is reading.
func (s *session) die(err error) error {
	if s.dead == nil {
		if e := strings.TrimSpace(s.errs.String()); e != "" {
			s.dead = fmt.Errorf("claude: %w: %s", err, e)
		} else {
			s.dead = fmt.Errorf("claude: %w", err)
		}
	}
	return s.dead
}

// writeJSON puts one event on the child's stdin. Held under its own lock because
// an interrupt is written while the reading side of a turn is still running.
func (s *session) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.stdin == nil {
		return errClosed
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

// interrupt cancels the turn in flight without killing the process, so the next
// Send still has the conversation — and its prompt cache — behind it.
func (s *session) interrupt() {
	s.reqs++
	_ = s.writeJSON(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("int_%d", s.reqs),
		"request":    map[string]any{"subtype": "interrupt"},
	})
}

// readLine reads one JSON event. bufio.Scanner is no good here: a line past its
// limit makes it stop, and with a persistent process a lost frame would corrupt
// every turn after this one rather than just this one, so an overlong line is an
// error that kills the session.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			if len(buf) > maxLine {
				return nil, fmt.Errorf("event longer than %d bytes: the stream has lost its framing", maxLine)
			}
			continue
		}
		if err != nil {
			return buf, err
		}
		return buf, nil
	}
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	content := strings.TrimSpace(in.Text)
	if len(in.ToolResults) > 0 {
		content = strings.TrimSpace(formatResults(in.ToolResults))
	}
	if content == "" {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	// One turn at a time. The next turn must not be written before this one's
	// result has been read: the CLI coalesces messages that arrive together, and
	// the two turns come back as one.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.start(); err != nil {
		return nil, err
	}
	if s.dead != nil {
		return nil, s.dead
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := s.writeJSON(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": content}},
		},
		"parent_tool_use_id": nil,
	}); err != nil {
		return nil, s.die(err)
	}

	// Turn-scoped cancellation: interrupt the turn, then keep reading until the
	// CLI reports it, so the stream stays framed for the next one.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			s.interrupt()
		case <-stopWatch:
		}
	}()

	var final, assistant strings.Builder
	var usage provider.Usage
	var apiTime time.Duration
	isErr, ended := false, false
	for !ended {
		line, err := readLine(s.out)
		if err != nil {
			if len(bytes.TrimSpace(line)) == 0 {
				return nil, s.die(err)
			}
			// A last event without its newline is still an event; take it, then
			// let the next read report the break.
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			if err != nil {
				return nil, s.die(err)
			}
			continue
		}
		var e streamEvent
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "system":
			if e.SessionID != "" {
				s.sessionID = e.SessionID
			}
		case "assistant":
			for _, b := range e.Message.Content {
				if b.Type == "text" {
					assistant.WriteString(b.Text)
					// Stream prose live (the driven CLI emits it in chunks), but
					// only when it's a normal reply — not the XML tool-call blocks
					// the driven model emits for dross to parse.
					if in.OnText != nil && s.driven && !strings.Contains(b.Text, "<function") && !strings.Contains(b.Text, "<invoke") {
						in.OnText(b.Text)
					}
				}
			}
			usage.InputTokens += e.Message.Usage.InputTokens
			usage.OutputTokens += e.Message.Usage.OutputTokens
			usage.CacheReadTokens += e.Message.Usage.CacheReadTokens
			usage.CacheWriteTokens += e.Message.Usage.CacheCreationTokens
		case "result":
			final.WriteString(e.Result)
			isErr = e.IsError
			apiTime = time.Duration(e.DurationAPIMs) * time.Millisecond
			u := e.Usage
			if u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 {
				usage.InputTokens, usage.OutputTokens = u.InputTokens, u.OutputTokens
				usage.CacheReadTokens, usage.CacheWriteTokens = u.CacheReadTokens, u.CacheCreationTokens
			}
			ended = true
		}
		if err != nil && !ended {
			return nil, s.die(err)
		}
	}

	// An interrupted turn ends with a result like any other; the caller asked for
	// the cancellation, so report that rather than whatever the CLI said.
	if err := ctx.Err(); err != nil {
		return nil, err
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
			return &provider.Response{Text: cleaned, ToolCalls: calls, StopReason: provider.StopToolUse, Usage: usage, APITime: apiTime}, nil
		}
		return &provider.Response{Text: cleaned, StopReason: provider.StopEndTurn, Usage: usage, APITime: apiTime}, nil
	}
	return &provider.Response{Text: out, StopReason: provider.StopEndTurn, Usage: usage, APITime: apiTime}, nil
}
