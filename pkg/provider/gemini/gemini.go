// Package gemini is a native backend for Google's Gemini models via the
// Generative Language API (generativelanguage.googleapis.com). It supports
// function calling and per-turn token accounting. Import for side effects to
// register:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/gemini"
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"golang.org/x/oauth2"

	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/pkg/provider/googleauth"
)

func init() {
	provider.Register("gemini", func(cfg provider.Config) (provider.Provider, error) {
		base := cfg.BaseURL
		if base == "" {
			base = "https://generativelanguage.googleapis.com/v1beta"
		}
		p := &prov{base: strings.TrimRight(base, "/"), hc: &http.Client{}}
		if cfg.APIKey != "" {
			p.apiKey = cfg.APIKey
			return p, nil
		}
		// No API key: fall back to the OAuth login (`dross login gemini`). The
		// gemini-cli OAuth token can't call generativelanguage (scope error), so
		// the OAuth path routes through the Code Assist endpoint instead (see Send).
		if ts, ok := googleauth.TokenSource(context.Background()); ok {
			p.ts = ts
			p.project = firstEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
			return p, nil
		}
		return nil, fmt.Errorf("gemini: no API key (set GEMINI_API_KEY) and no OAuth login — run `dross login gemini`")
	})
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

const defaultMaxTokens = 8192

// codeAssistBase is Google's Code Assist endpoint. The gemini-cli OAuth client is
// authorized for this API (not generativelanguage), so the OAuth path routes here.
const codeAssistBase = "https://cloudcode-pa.googleapis.com/v1internal"

type prov struct {
	base    string
	apiKey  string             // API-key auth (x-goog-api-key)
	ts      oauth2.TokenSource // OAuth auth (Bearer) when no API key
	project string             // GCP project for Code Assist (OAuth)
	mu      sync.Mutex         // guards lazy project discovery
	hc      *http.Client
}

func (p *prov) Name() string { return "gemini" }

// ensureProject resolves the Code Assist project for OAuth: the configured one,
// else auto-discovered via loadCodeAssist. Cached after first success.
func (p *prov) ensureProject(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.project != "" {
		return p.project, nil
	}
	tok, err := p.ts.Token()
	if err != nil {
		return "", fmt.Errorf("gemini: token refresh failed (re-run `dross login gemini`): %w", err)
	}
	body := []byte(`{"metadata":{"pluginType":"GEMINI"}}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, codeAssistBase+":loadCodeAssist", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := p.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var lc struct {
		CloudaicompanionProject string `json:"cloudaicompanionProject"`
	}
	_ = json.Unmarshal(raw, &lc)
	if lc.CloudaicompanionProject == "" {
		return "", fmt.Errorf("gemini (OAuth): no Code Assist project — set GOOGLE_CLOUD_PROJECT to a project with the Gemini for Google Cloud API enabled")
	}
	p.project = lc.CloudaicompanionProject
	return p.project, nil
}

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	s := &session{prov: p, model: cfg.Model, tools: convertTools(cfg.Tools), maxTok: maxTok, pending: map[string]string{}}
	if strings.TrimSpace(cfg.System) != "" {
		s.sys = &content{Parts: []part{{Text: cfg.System}}}
	}
	return s
}

// --- wire types (Generative Language API) ---------------------------------

type content struct {
	Role  string `json:"role,omitempty"` // "user" | "model"
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type functionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type toolWrap struct {
	FunctionDeclarations []funcDecl `json:"functionDeclarations"`
}

type funcDecl struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Parameters  *schema `json:"parameters,omitempty"`
}

type schema struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
	Required   []string       `json:"required,omitempty"`
}

type genReq struct {
	SystemInstruction *content        `json:"systemInstruction,omitempty"`
	Contents          []content       `json:"contents"`
	Tools             []toolWrap      `json:"tools,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// caReq is the Code Assist envelope for an OAuth generateContent call.
type caReq struct {
	Model   string `json:"model"`
	Project string `json:"project,omitempty"`
	Request genReq `json:"request"`
}

type genResp struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type session struct {
	prov     *prov
	model    string
	sys      *content
	tools    []toolWrap
	maxTok   int
	contents []content
	pending  map[string]string // tool-call id -> function name (for functionResponse)
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	// Append the user turn: tool results (as functionResponse) or free text.
	switch {
	case len(in.ToolResults) > 0:
		var parts []part
		for _, r := range in.ToolResults {
			name := s.pending[r.ID]
			if name == "" {
				name = r.ID
			}
			parts = append(parts, part{FunctionResponse: &functionResponse{
				Name: name, Response: map[string]any{"result": r.Content},
			}})
		}
		s.contents = append(s.contents, content{Role: "user", Parts: parts})
	case strings.TrimSpace(in.Text) != "":
		s.contents = append(s.contents, content{Role: "user", Parts: []part{{Text: in.Text}}})
	default:
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	genReqObj := genReq{
		SystemInstruction: s.sys,
		Contents:          s.contents,
		Tools:             s.tools,
		GenerationConfig:  generationConfig{MaxOutputTokens: s.maxTok},
	}

	oauth := s.prov.ts != nil
	var reqBody []byte
	var url string
	if oauth {
		// OAuth token is only authorized for Code Assist, not generativelanguage.
		// Wrap the request as Code Assist expects and target that endpoint.
		project, err := s.prov.ensureProject(ctx)
		if err != nil {
			return nil, err
		}
		reqBody, err = json.Marshal(caReq{Model: s.model, Project: project, Request: genReqObj})
		if err != nil {
			return nil, err
		}
		url = codeAssistBase + ":generateContent"
	} else {
		var err error
		reqBody, err = json.Marshal(genReqObj)
		if err != nil {
			return nil, err
		}
		url = s.prov.base + "/models/" + s.model + ":generateContent"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if oauth {
		tok, err := s.prov.ts.Token()
		if err != nil {
			return nil, fmt.Errorf("gemini: token refresh failed (re-run `dross login gemini`): %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	} else {
		httpReq.Header.Set("x-goog-api-key", s.prov.apiKey) // key in header, not the URL
	}

	resp, err := s.prov.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	// Code Assist wraps the GenerateContentResponse under a "response" key.
	payload := raw
	if oauth {
		var wrap struct {
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal(raw, &wrap) == nil && len(wrap.Response) > 0 {
			payload = wrap.Response
		}
	}

	var gr genResp
	if err := json.Unmarshal(payload, &gr); err != nil {
		return nil, fmt.Errorf("gemini: bad response (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if gr.Error != nil {
		return nil, fmt.Errorf("gemini: %s", gr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(gr.Candidates) == 0 {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	cand := gr.Candidates[0]
	s.contents = append(s.contents, cand.Content) // keep the model turn in history

	out := &provider.Response{Usage: provider.Usage{
		InputTokens:     gr.UsageMetadata.PromptTokenCount,
		OutputTokens:    gr.UsageMetadata.CandidatesTokenCount,
		CacheReadTokens: gr.UsageMetadata.CachedContentTokenCount,
	}}
	s.pending = map[string]string{}
	for i, p := range cand.Content.Parts {
		if p.Text != "" {
			out.Text += p.Text
		}
		if p.FunctionCall != nil {
			id := fmt.Sprintf("call_%d", i)
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{ID: id, Name: p.FunctionCall.Name, Input: args})
			s.pending[id] = p.FunctionCall.Name
		}
	}
	out.StopReason = mapFinish(cand.FinishReason, len(out.ToolCalls))
	return out, nil
}

func mapFinish(reason string, nCalls int) string {
	if nCalls > 0 {
		return provider.StopToolUse
	}
	switch reason {
	case "STOP":
		return provider.StopEndTurn
	case "MAX_TOKENS":
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}

// convertTools maps dross tool defs to Gemini functionDeclarations (all under a
// single tools entry).
func convertTools(defs []provider.ToolDef) []toolWrap {
	if len(defs) == 0 {
		return nil
	}
	fds := make([]funcDecl, 0, len(defs))
	for _, d := range defs {
		var params *schema
		if len(d.Properties) > 0 {
			params = &schema{Type: "object", Properties: d.Properties, Required: d.Required}
		}
		fds = append(fds, funcDecl{Name: d.Name, Description: d.Description, Parameters: params})
	}
	return []toolWrap{{FunctionDeclarations: fds}}
}
