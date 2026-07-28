// Package claudecode is a provider that drives the locally-installed Claude Code
// CLI (`claude`) in headless mode. It uses whatever auth the CLI already has —
// typically a Claude subscription — so holocron can talk to Claude without an
// Anthropic API key. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/holocron/internal/provider/claudecode"
//
// Unlike raw model backends, Claude Code runs its OWN agent loop and tools. This
// provider therefore ignores the holocron tools/system passed in SessionConfig,
// forwards the user's text, and returns Claude Code's final answer. Turns are
// linked with --resume so a session keeps context.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dougkirkley/holocron/internal/provider"
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
	// Claude Code owns permissioning; in headless mode it can't prompt, so
	// auto-accept edits. Set HOLOCRON_CLAUDE_DANGEROUS=1 to bypass all checks
	// (needed for unattended bash), which maps to --dangerously-skip-permissions.
	perm := "acceptEdits"
	if v := os.Getenv("HOLOCRON_CLAUDE_DANGEROUS"); v == "1" || v == "true" {
		perm = "" // signals bypass in Send
	}
	return &session{prov: p, model: model, permission: perm}
}

type session struct {
	prov       *prov
	model      string
	permission string
	sessionID  string // captured from the first turn, reused via --resume
}

// streamEvent is the subset of Claude Code's stream-json we read.
type streamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
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
	// Claude Code owns its own tools, so holocron never gets tool calls back and
	// thus never sends tool results. If it somehow does, there's nothing to add.
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	args := []string{"-p", text, "--output-format", "stream-json", "--verbose", "--model", s.model}
	if s.sessionID != "" {
		args = append(args, "--resume", s.sessionID)
	}
	if s.permission == "" {
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
		out = assistant.String() // fall back to streamed text
	}
	if isErr {
		return nil, fmt.Errorf("claude: %s", strings.TrimSpace(out))
	}
	return &provider.Response{Text: out, StopReason: provider.StopEndTurn, Usage: usage}, nil
}
