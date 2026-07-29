// Package vertex is a native backend for Gemini models on Google Cloud Vertex
// AI. It speaks the same generateContent request/response shape as the Gemini
// API but against the regional aiplatform endpoint, authenticated with Google
// Application Default Credentials (ADC) instead of an API key. Import for side
// effects to register:
//
//	import _ "github.com/dougkirkley/ergon/pkg/provider/vertex"
//
// Project and region come from VERTEX_PROJECT / GOOGLE_CLOUD_PROJECT and
// VERTEX_REGION / GOOGLE_CLOUD_LOCATION (default us-central1). Credentials come
// from the Google ADC chain (GOOGLE_APPLICATION_CREDENTIALS, gcloud login, or a
// workload identity / metadata server).
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dougkirkley/ergon/pkg/provider"
)

func init() {
	provider.Register("vertex", func(cfg provider.Config) (provider.Provider, error) {
		project := firstEnv("VERTEX_PROJECT", "GOOGLE_CLOUD_PROJECT")
		if project == "" {
			return nil, fmt.Errorf("vertex: set VERTEX_PROJECT or GOOGLE_CLOUD_PROJECT")
		}
		region := firstEnv("VERTEX_REGION", "GOOGLE_CLOUD_LOCATION")
		if region == "" {
			region = "us-central1"
		}
		ts, err := google.DefaultTokenSource(context.Background(), "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("vertex: no Google credentials (ADC): %w", err)
		}
		base := cfg.BaseURL
		if base == "" {
			host := region + "-aiplatform.googleapis.com"
			if region == "global" {
				host = "aiplatform.googleapis.com"
			}
			base = fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/publishers/google", host, project, region)
		}
		return &prov{base: strings.TrimRight(base, "/"), ts: ts, hc: &http.Client{}}, nil
	})
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

const defaultMaxTokens = 8192

type prov struct {
	base string
	ts   oauth2.TokenSource
	hc   *http.Client
}

func (p *prov) Name() string { return "vertex" }

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

// --- wire types (generateContent; shared shape with the Gemini API) --------

type content struct {
	Role  string `json:"role,omitempty"`
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
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	Tools             []toolWrap       `json:"tools,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
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
	pending  map[string]string
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	switch {
	case len(in.ToolResults) > 0:
		var parts []part
		for _, r := range in.ToolResults {
			name := s.pending[r.ID]
			if name == "" {
				name = r.ID
			}
			parts = append(parts, part{FunctionResponse: &functionResponse{Name: name, Response: map[string]any{"result": r.Content}}})
		}
		s.contents = append(s.contents, content{Role: "user", Parts: parts})
	case strings.TrimSpace(in.Text) != "":
		s.contents = append(s.contents, content{Role: "user", Parts: []part{{Text: in.Text}}})
	default:
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	reqBody, err := json.Marshal(genReq{
		SystemInstruction: s.sys,
		Contents:          s.contents,
		Tools:             s.tools,
		GenerationConfig:  generationConfig{MaxOutputTokens: s.maxTok},
	})
	if err != nil {
		return nil, err
	}
	tok, err := s.prov.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex: token: %w", err)
	}
	url := s.prov.base + "/models/" + s.model + ":generateContent"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	tok.SetAuthHeader(httpReq)

	resp, err := s.prov.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var gr genResp
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, fmt.Errorf("vertex: bad response (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if gr.Error != nil {
		return nil, fmt.Errorf("vertex: %s", gr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vertex: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(gr.Candidates) == 0 {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	cand := gr.Candidates[0]
	s.contents = append(s.contents, cand.Content)
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
