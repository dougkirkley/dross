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
	"time"

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
	s := &session{prov: p, model: cfg.Model, tools: convertTools(cfg.Tools), maxTokens: maxTok,
		temperature: cfg.Temperature, seed: cfg.Seed}
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
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Tools       []toolSpec    `json:"tools,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Seed        *int          `json:"seed,omitempty"`
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
	prov        *prov
	model       string
	tools       []toolSpec
	maxTokens   int
	temperature *float64
	seed        *int
	messages    []chatMessage
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
		Model:       s.model,
		Messages:    s.messages,
		Tools:       s.tools,
		MaxTokens:   s.maxTokens,
		Temperature: s.temperature,
		Seed:        s.seed,
	}
	// Stream when the caller wants to watch. A local model produces tokens for
	// the whole generation; buffering them meant forty-five seconds of silence
	// followed by the entire answer, which reads as a hang and then a dump.
	if in.OnText != nil {
		req.Stream = true
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	reqBody, _ := json.Marshal(req)

	// An empty answer is a failed turn, not a finished one, so it is re-asked
	// like any other transient failure — see emptyResponse.
	mark := len(s.messages)
	for attempt := 1; ; attempt++ {
		out, err := s.attempt(ctx, reqBody, req.Stream, in.OnText)
		if err != nil {
			return nil, err
		}
		if !emptyResponse(out) || attempt >= sendAttempts {
			return out, nil
		}
		// Drop the empty assistant turn before re-asking, or the history grows
		// a message that says nothing and the next attempt is asked to continue
		// from it.
		s.messages = s.messages[:mark]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryPause(attempt)):
		}
	}
}

// emptyResponse reports a turn that said nothing and did nothing.
//
// A local server can answer 200 with a completion containing no text, no tool
// call and no tokens at all. Taken at face value that reads to the agent loop
// as "the model has decided it is finished", so a task ends in one turn having
// done nothing, and reports success. That was watched happening: in=0, out=0,
// one turn, 1.6 seconds, the bug still there.
//
// Re-asking is right because it is not a decision the model made. A model that
// genuinely has nothing left to do says so in words.
func emptyResponse(r *provider.Response) bool {
	return r != nil && strings.TrimSpace(r.Text) == "" && len(r.ToolCalls) == 0 &&
		r.Usage.OutputTokens == 0
}

// attempt performs one request and shapes the reply.
func (s *session) attempt(ctx context.Context, reqBody []byte, stream bool, onText func(string)) (*provider.Response, error) {
	resp, err := s.post(ctx, reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if stream {
		return s.readStream(resp.Body, onText)
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

// sendAttempts is how many times one turn will be asked for.
//
// A local server fails a whole turn for reasons that are not the turn's fault:
// ollama answers 500 when the model emits a tool call it cannot parse, and a
// shared GPU answers 503 while another job holds it. Both clear on a re-ask —
// the second sample is a different sample. Without this, one bad token from a
// small model ends a run that is otherwise minutes in, which is the most
// expensive kind of slow there is.
//
// Three attempts, not more: past that the model is not having a bad roll, it
// is producing something the server genuinely cannot accept, and the error is
// worth showing.
const sendAttempts = 3

// retryPause is the wait before re-asking. Short on purpose — somebody is
// watching a cursor blink.
func retryPause(attempt int) time.Duration { return time.Duration(attempt) * 400 * time.Millisecond }

// post sends one completion request, re-asking on the failures that a re-ask
// fixes. The returned response's body is still open and belongs to the caller.
func (s *session) post(ctx context.Context, reqBody []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= sendAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryPause(attempt - 1)):
			}
		}
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
			// A cancelled context is the user's decision, not a flaky server.
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastErr = fmt.Errorf("openai: %s: %s", resp.Status, truncate(body, 300))
		// The server rejected the model's own output rather than failing at
		// something. Re-asking produces another sample of the same confusion —
		// watched failing three times running — so it goes back to the caller
		// immediately, tagged as the one error the model itself can fix.
		if malformedToolCall(body) {
			return nil, fmt.Errorf("%w: %s", provider.ErrMalformedToolCall, truncate(body, 300))
		}
		if !retryableStatus(resp.StatusCode) {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// malformedToolCall spots the server complaining that it could not parse the
// tool call the model emitted, as opposed to the server itself being unwell.
func malformedToolCall(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte("parsing tool call"))
}

// retryableStatus reports whether asking again could plausibly answer
// differently: the server is overloaded, wedged, or rejected a sample it will
// not be given twice. A 4xx other than rate-limiting is a request the server
// will refuse identically, so it fails now rather than three times as slowly.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
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
		if w.Name == "" && len(w.Args) == 0 {
			// No name and no arguments field: the model may have written the
			// arguments object on its own, which is the same mistake one step
			// further along.
			if tc, ok := s.callFromArgsOnly(body); ok {
				return []toolCall{tc}, ""
			}
			return nil, content
		}
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

// callFromArgsOnly recovers a call from an arguments object with no name on it.
//
// gpt-oss:20b does this: given the bash tool it writes
// {"background":false,"command":"which kubebuilder"} into the content and stops.
// The turn ends having announced work it never did, and the raw object is
// rendered as though it were the answer.
//
// The name is inferred from the shape, and only when the shape can mean one
// thing: every key is a declared property of exactly one offered tool, that
// tool's required properties are all present, and no other tool matches. A
// model answering a question *with* JSON does not happen to produce an object
// whose keys are precisely one tool's parameters — and if two tools could take
// it, nothing here is willing to guess which.
func (s *session) callFromArgsOnly(body string) (toolCall, bool) {
	var args map[string]any
	if json.Unmarshal([]byte(body), &args) != nil || len(args) == 0 {
		return toolCall{}, false
	}
	var found string
	for _, t := range s.tools {
		props, _ := t.Function.Parameters["properties"].(map[string]any)
		if len(props) == 0 {
			continue
		}
		fits := true
		for k := range args {
			if _, ok := props[k]; !ok {
				fits = false
				break
			}
		}
		for _, r := range required(t.Function.Parameters) {
			if _, ok := args[r]; !ok {
				fits = false
				break
			}
		}
		if !fits {
			continue
		}
		if found != "" {
			return toolCall{}, false // ambiguous; guessing is worse than prose
		}
		found = t.Function.Name
	}
	if found == "" {
		return toolCall{}, false
	}
	var tc toolCall
	tc.ID = "call_json_args"
	tc.Type = "function"
	tc.Function.Name = found
	tc.Function.Arguments = body
	return tc, true
}

// required lists a tool's required property names.
func required(params map[string]any) []string {
	raw, _ := params["required"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
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
