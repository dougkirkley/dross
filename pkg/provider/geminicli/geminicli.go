// Package geminicli is a Gemini backend that reuses the Google Gemini CLI's
// existing OAuth login, so a machine already authenticated with `gemini` (its
// "Login with Google" flow) can use Gemini from dross with zero extra setup —
// no API key. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/geminicli"
//
// It reads the cached credentials the CLI writes to ~/.gemini/oauth_creds.json,
// refreshes the access token with the CLI's public OAuth client, resolves the
// Code Assist project/tier (loadCodeAssist / onboardUser), and calls the same
// Code Assist endpoint (cloudcode-pa.googleapis.com) the CLI uses for OAuth
// users. Wire values verified against google-gemini/gemini-cli source.
package geminicli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"

	"github.com/dougkirkley/dross/pkg/provider"
)

// Public OAuth client the Gemini CLI embeds (packages/core/src/code_assist/
// oauth2.ts). Not a secret — shipped in the CLI binary — used only to refresh
// the user's own cached refresh token.
const (
	oauthClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	oauthClientSecret = "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"
	oauthTokenURL     = "https://oauth2.googleapis.com/token"

	caEndpoint = "https://cloudcode-pa.googleapis.com/v1internal"
)

func init() {
	provider.Register("gemini-cli", func(cfg provider.Config) (provider.Provider, error) {
		creds, err := loadOAuthCreds()
		if err != nil {
			return nil, err
		}
		conf := &oauth2.Config{
			ClientID:     oauthClientID,
			ClientSecret: oauthClientSecret,
			Endpoint:     oauth2.Endpoint{TokenURL: oauthTokenURL, AuthStyle: oauth2.AuthStyleInParams},
			Scopes: []string{
				"https://www.googleapis.com/auth/cloud-platform",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
			},
		}
		tok := &oauth2.Token{
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			TokenType:    creds.TokenType,
		}
		if creds.ExpiryDate > 0 {
			tok.Expiry = time.UnixMilli(creds.ExpiryDate)
		}
		if tok.RefreshToken == "" {
			return nil, fmt.Errorf("gemini-cli: %s has no refresh_token — re-run `gemini` login", credsPath())
		}
		// oauth2 refreshes automatically once the access token expires.
		ts := conf.TokenSource(context.Background(), tok)
		// Project from the same env vars the CLI honors (optional; free tier gets a
		// server-managed project). Numeric ids are rejected by Code Assist.
		project := firstEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID")
		return &prov{ts: ts, project: project, hc: &http.Client{Timeout: 120 * time.Second}}, nil
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

// oauthCreds mirrors the google-auth-library Credentials object the CLI writes.
type oauthCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
	ExpiryDate   int64  `json:"expiry_date"` // epoch milliseconds
}

func credsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".gemini", "oauth_creds.json")
}

func loadOAuthCreds() (*oauthCreds, error) {
	p := credsPath()
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("gemini-cli: no Gemini CLI login found at %s — run `gemini` and sign in first (%v)", p, err)
	}
	var c oauthCreds
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("gemini-cli: cannot parse %s: %w", p, err)
	}
	return &c, nil
}

const defaultMaxTokens = 8192

type prov struct {
	ts      oauth2.TokenSource
	hc      *http.Client
	project string

	mu       sync.Mutex
	resolved bool // project/tier resolved via loadCodeAssist
	tier     string
}

func (p *prov) Name() string { return "gemini-cli" }

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

// --- auth + Code Assist plumbing ------------------------------------------

func (p *prov) authHeader(ctx context.Context, req *http.Request) error {
	tok, err := p.ts.Token()
	if err != nil {
		return fmt.Errorf("gemini-cli: token refresh failed (re-run `gemini` login): %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func (p *prov) postCA(ctx context.Context, method string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, caEndpoint+":"+method, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if err := p.authHeader(ctx, req); err != nil {
		return err
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gemini-cli: %s http %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("gemini-cli: %s bad response: %s", method, strings.TrimSpace(string(raw)))
		}
	}
	return nil
}

// resolveProject discovers the Code Assist project/tier once (loadCodeAssist),
// onboarding the user if needed. Free-tier users get a server-managed project.
func (p *prov) resolveProject(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resolved {
		return nil
	}
	md := map[string]any{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	}
	if p.project != "" {
		md["duetProject"] = p.project
	}
	load := map[string]any{"metadata": md}
	if p.project != "" {
		load["cloudaicompanionProject"] = p.project
	}
	var lr struct {
		CurrentTier             *struct{ ID string `json:"id"` } `json:"currentTier"`
		CloudaicompanionProject string                           `json:"cloudaicompanionProject"`
		AllowedTiers            []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if err := p.postCA(ctx, "loadCodeAssist", load, &lr); err != nil {
		return err
	}
	if lr.CurrentTier != nil {
		// Already onboarded.
		if p.project == "" {
			p.project = lr.CloudaicompanionProject
		}
		p.tier = lr.CurrentTier.ID
		p.resolved = true
		return nil
	}
	// Not onboarded: pick the default tier and onboard.
	tierID := "free-tier"
	for _, t := range lr.AllowedTiers {
		if t.IsDefault {
			tierID = t.ID
			break
		}
	}
	onboardMD := map[string]any{"pluginType": "GEMINI"}
	if p.project != "" {
		onboardMD["duetProject"] = p.project
	}
	onboard := map[string]any{"tierId": tierID, "metadata": onboardMD}
	if p.project != "" {
		onboard["cloudaicompanionProject"] = p.project
	}
	// onboardUser is a long-running op; poll until done.
	for attempt := 0; attempt < 12; attempt++ {
		var or struct {
			Done     bool `json:"done"`
			Response struct {
				CloudaicompanionProject struct {
					ID string `json:"id"`
				} `json:"cloudaicompanionProject"`
			} `json:"response"`
		}
		if err := p.postCA(ctx, "onboardUser", onboard, &or); err != nil {
			return err
		}
		if or.Done {
			if p.project == "" {
				p.project = or.Response.CloudaicompanionProject.ID
			}
			p.tier = tierID
			p.resolved = true
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	// Onboarding didn't converge; proceed with whatever project we have (free
	// tier tolerates an empty project — the server manages one).
	p.tier = tierID
	p.resolved = true
	return nil
}

// --- wire types (Gemini generateContent, wrapped for Code Assist) ---------

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

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// innerReq is the standard Gemini request; caReq wraps it for Code Assist.
type innerReq struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	Tools             []toolWrap       `json:"tools,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type caReq struct {
	Model        string   `json:"model"`
	Project      string   `json:"project,omitempty"`
	UserPromptID string   `json:"user_prompt_id,omitempty"`
	Request      innerReq `json:"request"`
}

// caResp nests the real Gemini response under "response".
type caResp struct {
	Response struct {
		Candidates []struct {
			Content      content `json:"content"`
			FinishReason string  `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	} `json:"response"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

var promptSeq int64

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

	if err := s.prov.resolveProject(ctx); err != nil {
		return nil, err
	}

	body := caReq{
		Model:        s.model,
		Project:      s.prov.project,
		UserPromptID: fmt.Sprintf("dross-%d", atomic.AddInt64(&promptSeq, 1)),
		Request: innerReq{
			SystemInstruction: s.sys,
			Contents:          s.contents,
			Tools:             s.tools,
			GenerationConfig:  generationConfig{MaxOutputTokens: s.maxTok},
		},
	}
	var cr caResp
	if err := s.prov.postCA(ctx, "generateContent", body, &cr); err != nil {
		return nil, err
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("gemini-cli: %s", cr.Error.Message)
	}
	if len(cr.Response.Candidates) == 0 {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	cand := cr.Response.Candidates[0]
	s.contents = append(s.contents, cand.Content)

	out := &provider.Response{Usage: provider.Usage{
		InputTokens:     cr.Response.UsageMetadata.PromptTokenCount,
		OutputTokens:    cr.Response.UsageMetadata.CandidatesTokenCount,
		CacheReadTokens: cr.Response.UsageMetadata.CachedContentTokenCount,
	}}
	s.pending = map[string]string{}
	for i, pt := range cand.Content.Parts {
		if pt.Text != "" {
			out.Text += pt.Text
		}
		if pt.FunctionCall != nil {
			id := fmt.Sprintf("call_%d", i)
			args := pt.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{ID: id, Name: pt.FunctionCall.Name, Input: args})
			s.pending[id] = pt.FunctionCall.Name
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
