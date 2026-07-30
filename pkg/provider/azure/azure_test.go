package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func TestConvertToolsAndFinish(t *testing.T) {
	td := convertTools([]provider.ToolDef{{Name: "read", Required: []string{"path"}, Properties: map[string]any{"path": map[string]any{"type": "string"}}}})
	if len(td) != 1 || td[0].Type != "function" || td[0].Function.Name != "read" {
		t.Fatalf("convertTools: %+v", td)
	}
	if mapFinish("stop", 1) != provider.StopToolUse || mapFinish("stop", 0) != provider.StopEndTurn || mapFinish("length", 0) != provider.StopMaxTokens {
		t.Fatal("mapFinish wrong")
	}
}

func TestAzureSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "k" {
			t.Errorf("missing api-key header")
		}
		if !strings.Contains(r.URL.Path, "/openai/deployments/my-gpt4o/chat/completions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("api-version") == "" {
			t.Errorf("missing api-version")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "hi from azure"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 2},
		})
	}))
	defer srv.Close()

	// Endpoint via config; deployment name is the model.
	p, err := provider.New("azure", provider.Config{BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	s := p.NewSession(provider.SessionConfig{Model: "my-gpt4o", System: "terse"})
	resp, err := s.Send(context.Background(), provider.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi from azure" || resp.StopReason != provider.StopEndTurn || resp.Usage.InputTokens != 7 {
		t.Fatalf("resp = %+v", resp)
	}
}
