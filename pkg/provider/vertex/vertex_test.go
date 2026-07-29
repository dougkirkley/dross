package vertex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dougkirkley/ergon/pkg/provider"
)

func TestConvertToolsAndFinish(t *testing.T) {
	tw := convertTools([]provider.ToolDef{{Name: "read", Properties: map[string]any{"path": map[string]any{"type": "string"}}, Required: []string{"path"}}})
	if len(tw) != 1 || tw[0].FunctionDeclarations[0].Name != "read" || tw[0].FunctionDeclarations[0].Parameters.Type != "object" {
		t.Fatalf("convertTools: %+v", tw)
	}
	if mapFinish("STOP", 1) != provider.StopToolUse || mapFinish("STOP", 0) != provider.StopEndTurn || mapFinish("MAX_TOKENS", 0) != provider.StopMaxTokens {
		t.Fatal("mapFinish mapping wrong")
	}
}

func TestVertexSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "hi from vertex"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 5, "candidatesTokenCount": 2},
		})
	}))
	defer srv.Close()

	p := &prov{base: srv.URL, ts: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), hc: srv.Client()}
	s := p.NewSession(provider.SessionConfig{Model: "gemini-2.5-pro", System: "terse"})
	resp, err := s.Send(context.Background(), provider.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi from vertex" || resp.StopReason != provider.StopEndTurn || resp.Usage.InputTokens != 5 {
		t.Fatalf("resp = %+v", resp)
	}
}
