// Package provider is a model-agnostic abstraction over LLM backends plus a
// driver-style registry. Concrete backends (anthropic, ollama, ...) live in
// sub-packages that register themselves in init(); import them for side effects
// (`import _ ".../internal/provider/anthropic"`) to make them available by name.
//
// The interface is turn-based: an agent opens a Session and calls Send once per
// turn, feeding tool results back in. The Session owns the provider-native
// message history so the agent stays free of any SDK types — which is what lets
// a local ollama model and a frontier Anthropic model sit behind one interface.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Config holds backend construction options. Kept minimal; grows per backend.
type Config struct {
	APIKey  string
	BaseURL string
}

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]any // JSON Schema "properties"
	Required    []string
}

// ToolCall is a model request to run a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the outcome of running a ToolCall, fed back on the next turn.
type ToolResult struct {
	ID      string
	Content string
	IsError bool
}

// Input is one turn's user-side content: free text, or tool results, not both.
type Input struct {
	Text        string
	ToolResults []ToolResult
	// OnText, if set, is called with incremental assistant-text chunks as they
	// stream in, so callers can render prose live instead of waiting for the
	// whole turn. Optional; providers that can't stream simply don't call it.
	OnText func(string)
}

// Usage is per-turn token accounting, summed across a run for cost reporting.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Add accumulates another Usage into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

// Stop reasons, normalized across backends.
const (
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopOther     = "other"
)

// Response is one assistant turn: any text, any tool calls, why it stopped.
type Response struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage

	// APITime is how long the backend says the model itself took, when it says
	// so at all (zero otherwise). It exists to separate the model's latency
	// from the cost of reaching it: a provider that shells out to a CLI pays a
	// process start on every turn, and without this that shows up as the model
	// being slow rather than as something worth fixing.
	APITime time.Duration
}

// SessionConfig is fixed for a conversation's lifetime.
type SessionConfig struct {
	System    string
	Tools     []ToolDef
	Model     string
	MaxTokens int

	// Temperature is the sampling temperature, when the caller has an opinion.
	//
	// A pointer because "no opinion" and "zero" are different requests, and only
	// the first should leave the field off the wire. Nothing set it for a long
	// time, so every backend's own default applied — around 0.7 on ollama — and
	// the same task took a different path on every run. That is right for prose
	// and wrong for a tool-calling loop, where the variation shows up as a
	// different number of turns rather than a different turn of phrase.
	Temperature *float64

	// Seed pins the sampler, where the backend supports one (ollama and OpenAI
	// do; Anthropic does not). It makes a run repeatable, which is what a
	// benchmark needs to tell a change from a roll of the dice.
	Seed *int
}

// ErrMalformedToolCall reports that the backend rejected the model's own
// output because it could not parse the tool call in it.
//
// It is separated from every other failure because it is the only one the model
// can fix. ollama answers 500 for this, and a plain retry re-asks the same
// question and gets a similarly malformed answer — three attempts, three
// failures, and a run that was minutes in ends with nothing. Told what
// happened, the model simply writes the call again.
var ErrMalformedToolCall = errors.New("backend could not parse the model's tool call")

// Session is one stateful conversation with a backend.
type Session interface {
	// Send appends the input as a user turn and returns the assistant response.
	Send(ctx context.Context, in Input) (*Response, error)
}

// Provider is a constructed backend that can open sessions.
type Provider interface {
	Name() string
	NewSession(SessionConfig) Session
}

// Factory builds a Provider from Config. Registered by backend packages.
type Factory func(Config) (Provider, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a backend factory under name. Called from a backend's init().
// Panics on duplicate name, matching database/sql driver semantics.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("provider: Register called twice for " + name)
	}
	registry[name] = f
}

// New constructs the named provider. The backend package must be imported so
// its init() has run.
func New(name string, cfg Config) (Provider, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q not registered (missing blank import?)", name)
	}
	return f(cfg)
}

// Names lists registered backends, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
