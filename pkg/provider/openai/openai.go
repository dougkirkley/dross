// Package openai is an OpenAI-compatible backend for the provider registry. It
// speaks the /chat/completions API, so it works against OpenAI itself and any
// compatible server — notably the homelab's ollama, reached by pointing
// Config.BaseURL at its /v1 endpoint. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/openai"
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/dougkirkley/dross/pkg/provider"
)

func init() {
	provider.Register("openai", func(cfg provider.Config) (provider.Provider, error) {
		base := cfg.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return &prov{
			base:   strings.TrimRight(base, "/"),
			apiKey: cfg.APIKey,
			hc:     &http.Client{}, // no timeout: generations are long
		}, nil
	})
}

const defaultMaxTokens = 16000

type prov struct {
	base   string
	apiKey string
	hc     *http.Client
}

func (p *prov) Name() string { return "openai" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	s := &session{prov: p, model: cfg.Model, tools: convertTools(cfg.Tools), maxTokens: maxTok}
	if cfg.System != "" {
		s.messages = append(s.messages, chatMessage{Role: "system", Content: cfg.System})
	}
	return s
}

// --- wire types (OpenAI chat-completions) ---------------------------------

type chatMessage struct {
	Role string `json:"role"`
	// Always sent, never omitted. A message whose content is absent arrives as
	// null, and ollama refuses the whole request with "invalid message content
	// type: <nil>" — an empty string is accepted. This bites a turn late: a
	// reply that is only tool calls, or only a thinking trace, has no text, so
	// it is stored with none and the *next* request is the one that fails.
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON encoded as a string
	} `json:"function"`
}

type toolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Tools     []toolSpec    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
	// StreamOptions asks for the usage numbers a streaming response otherwise
	// omits; ollama and OpenAI both honour it.
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// --- session --------------------------------------------------------------

type session struct {
	prov      *prov
	model     string
	tools     []toolSpec
	maxTokens int
	messages  []chatMessage
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	if len(in.ToolResults) > 0 {
		for _, r := range in.ToolResults {
			s.messages = append(s.messages, chatMessage{Role: "tool", ToolCallID: r.ID, Content: r.Content})
		}
	} else {
		s.messages = append(s.messages, chatMessage{Role: "user", Content: in.Text})
	}

	req := chatRequest{
		Model:     s.model,
		Messages:  s.messages,
		Tools:     s.tools,
		MaxTokens: s.maxTokens,
	}
	// Stream when the caller wants to watch. A local model produces tokens for
	// the whole generation; buffering them meant forty-five seconds of silence
	// followed by the entire answer, which reads as a hang and then a dump.
	if in.OnText != nil {
		req.Stream = true
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	reqBody, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.prov.base+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if s.prov.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.prov.apiKey)
	}

	resp, err := s.prov.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai: %s: %s", resp.Status, truncate(body, 300))
	}
	if req.Stream {
		return s.readStream(resp.Body, in.OnText)
	}

	body, _ := io.ReadAll(resp.Body)
	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("openai: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}
	return s.finish(cr.Choices[0].Message, cr.Choices[0].FinishReason, provider.Usage{
		InputTokens:  cr.Usage.PromptTokens,
		OutputTokens: cr.Usage.CompletionTokens,
	}), nil
}

// finish normalizes an assistant message, applies the XML tool-call fallback,
// records it in the conversation, and shapes the provider response. Shared by
// the buffered and streaming paths so they cannot drift.
func (s *session) finish(msg chatMessage, finishReason string, usage provider.Usage) *provider.Response {
	msg.Role = "assistant" // normalize before appending to history

	// Some ollama models (e.g. qwen3-coder) emit tool calls as XML text in the
	// content instead of structured tool_calls. Parse that fallback so the tool
	// actually runs, and rewrite the stored message so tool-result IDs match.
	stop := normStop(finishReason)
	if len(msg.ToolCalls) == 0 {
		if synth, cleaned := parseXMLToolCalls(msg.Content); len(synth) > 0 {
			msg.ToolCalls = synth
			msg.Content = cleaned
			stop = provider.StopToolUse
		} else if synth, cleaned := s.parseJSONToolCalls(msg.Content); len(synth) > 0 {
			msg.ToolCalls = synth
			msg.Content = cleaned
			stop = provider.StopToolUse
		}
	}
	s.messages = append(s.messages, msg)

	out := &provider.Response{Text: msg.Content, StopReason: stop, Usage: usage}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(args),
		})
	}
	return out
}

// streamChunk is one SSE data frame of a streaming completion.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readStream consumes an SSE completion, forwarding text deltas as they arrive
// and accumulating the full message for the conversation history.
//
// Tool-call deltas accumulate by index: the id and name arrive once, the
// arguments in fragments. Usage rides in a final chunk with no choices, which
// stream_options asked for.
func (s *session) readStream(body io.Reader, onText func(string)) (*provider.Response, error) {
	var (
		content strings.Builder
		calls   = map[int]*toolCall{}
		finish  string
		usage   provider.Usage
		sc      = bufio.NewScanner(body)
	)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue // a malformed frame is not worth killing the answer over
		}
		if ch.Error != nil {
			return nil, fmt.Errorf("openai: %s", ch.Error.Message)
		}
		if ch.Usage != nil {
			usage.InputTokens = ch.Usage.PromptTokens
			usage.OutputTokens = ch.Usage.CompletionTokens
		}
		if len(ch.Choices) == 0 {
			continue
		}
		c := ch.Choices[0]
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Delta.Content != "" {
			content.WriteString(c.Delta.Content)
			onText(c.Delta.Content)
		}
		for _, d := range c.Delta.ToolCalls {
			tc := calls[d.Index]
			if tc == nil {
				tc = &toolCall{}
				calls[d.Index] = tc
			}
			if d.ID != "" {
				tc.ID = d.ID
			}
			if d.Type != "" {
				tc.Type = d.Type
			}
			if d.Function.Name != "" {
				tc.Function.Name = d.Function.Name
			}
			tc.Function.Arguments += d.Function.Arguments
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("openai: stream: %w", err)
	}

	msg := chatMessage{Content: content.String()}
	for i := 0; i < len(calls); i++ {
		if tc := calls[i]; tc != nil {
			msg.ToolCalls = append(msg.ToolCalls, *tc)
		}
	}
	return s.finish(msg, finish, usage), nil
}

// jsonFence strips a ```json … ``` wrapper, which models add as often as not.
var jsonFence = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")

// parseJSONToolCalls recovers a tool call a model wrote into its message text
// instead of the structured tool_calls field.
//
// qwen2.5-coder does this: given a tool definition it produces exactly the
// right call — correct name, correct arguments — as a bare JSON object in the
// content, and leaves tool_calls null. The agent loop then sees prose, calls
// nothing, and ends the turn having announced work it never did. A Code
// session with that model could not write a file at all.
//
// The guard against reading an ordinary JSON answer as a call is the tool
// name: only an object naming a tool this session actually offered counts.
// A model answering a question *about* JSON does not happen to name one of
// our tools at the top level of an object with an arguments field.
func (s *session) parseJSONToolCalls(content string) ([]toolCall, string) {
	body := strings.TrimSpace(content)
	if m := jsonFence.FindStringSubmatch(body); m != nil {
		body = strings.TrimSpace(m[1])
	}
	if !strings.HasPrefix(body, "{") && !strings.HasPrefix(body, "[") {
		return nil, content
	}
	known := make(map[string]bool, len(s.tools))
	for _, t := range s.tools {
		known[t.Function.Name] = true
	}
	if len(known) == 0 {
		return nil, content
	}

	type wire struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	}
	var list []wire
	if strings.HasPrefix(body, "[") {
		if json.Unmarshal([]byte(body), &list) != nil {
			return nil, content
		}
	} else {
		var one wire
		if json.Unmarshal([]byte(body), &one) != nil {
			return nil, content
		}
		list = []wire{one}
	}

	var calls []toolCall
	for i, w := range list {
		if !known[w.Name] || len(w.Args) == 0 {
			return nil, content // not ours; leave the text alone
		}
		var tc toolCall
		tc.ID = fmt.Sprintf("call_json_%d", i)
		tc.Type = "function"
		tc.Function.Name = w.Name
		tc.Function.Arguments = string(w.Args)
		calls = append(calls, tc)
	}
	// The whole message was the call, so nothing is left to say.
	return calls, ""
}

// XML tool-call syntax emitted by some ollama models (qwen3-coder etc.):
//
//	<tool_call>
//	<function=bash>
//	<parameter=command>which go</parameter>
//	</function>
//	</tool_call>
var (
	xmlFuncRE  = regexp.MustCompile(`(?s)<function=([^>\s]+)\s*>(.*?)</function>`)
	xmlParamRE = regexp.MustCompile(`(?s)<parameter=([^>\s]+)\s*>(.*?)</parameter>`)
	xmlStripRE = regexp.MustCompile(`(?s)</?tool_call>|<function=[^>]*>.*?</function>`)
)

// parseXMLToolCalls extracts XML-style tool calls from assistant content and
// returns them plus the content with those blocks removed. Values are treated
// as strings, which fits our string-valued tool params (command, path, content).
func parseXMLToolCalls(content string) ([]toolCall, string) {
	if !strings.Contains(content, "<function=") {
		return nil, content
	}
	var calls []toolCall
	for i, m := range xmlFuncRE.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(m[1])
		args := map[string]string{}
		for _, p := range xmlParamRE.FindAllStringSubmatch(m[2], -1) {
			args[strings.TrimSpace(p[1])] = strings.TrimSpace(p[2])
		}
		b, err := json.Marshal(args)
		if err != nil {
			continue
		}
		var tc toolCall
		tc.ID = fmt.Sprintf("call_xml_%d", i)
		tc.Type = "function"
		tc.Function.Name = name
		tc.Function.Arguments = string(b)
		calls = append(calls, tc)
	}
	if len(calls) == 0 {
		return nil, content
	}
	cleaned := strings.TrimSpace(xmlStripRE.ReplaceAllString(content, ""))
	return calls, cleaned
}

func convertTools(defs []provider.ToolDef) []toolSpec {
	out := make([]toolSpec, 0, len(defs))
	for _, d := range defs {
		var t toolSpec
		t.Type = "function"
		t.Function.Name = d.Name
		t.Function.Description = d.Description
		t.Function.Parameters = map[string]any{
			"type":       "object",
			"properties": d.Properties,
			"required":   d.Required,
		}
		out = append(out, t)
	}
	return out
}

func normStop(r string) string {
	switch r {
	case "stop":
		return provider.StopEndTurn
	case "tool_calls":
		return provider.StopToolUse
	case "length":
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
