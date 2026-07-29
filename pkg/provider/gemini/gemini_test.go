package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dougkirkley/ergon/pkg/provider"
)

func TestConvertTools(t *testing.T) {
	tw := convertTools([]provider.ToolDef{{
		Name: "read", Description: "read a file",
		Properties: map[string]any{"path": map[string]any{"type": "string"}},
		Required:   []string{"path"},
	}})
	if len(tw) != 1 || len(tw[0].FunctionDeclarations) != 1 {
		t.Fatalf("convertTools shape: %+v", tw)
	}
	fd := tw[0].FunctionDeclarations[0]
	if fd.Name != "read" || fd.Parameters == nil || fd.Parameters.Type != "object" || len(fd.Parameters.Required) != 1 {
		t.Fatalf("funcDecl wrong: %+v", fd)
	}
}

func TestMapFinish(t *testing.T) {
	if mapFinish("STOP", 1) != provider.StopToolUse {
		t.Fatal("tool calls should map to tool_use")
	}
	if mapFinish("STOP", 0) != provider.StopEndTurn {
		t.Fatal("STOP -> end_turn")
	}
	if mapFinish("MAX_TOKENS", 0) != provider.StopMaxTokens {
		t.Fatal("MAX_TOKENS -> max_tokens")
	}
}

func TestSendParsesTextAndToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("missing api key header")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "systemInstruction") {
			t.Errorf("system not sent: %s", body)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{
					{"text": "reading it"},
					{"functionCall": map[string]any{"name": "read", "args": map[string]any{"path": "go.mod"}}},
				}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 12, "candidatesTokenCount": 3},
		})
	}))
	defer srv.Close()

	p, err := provider.New("gemini", provider.Config{BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	s := p.NewSession(provider.SessionConfig{Model: "gemini-2.5-pro", System: "be terse", Tools: []provider.ToolDef{{Name: "read"}}})
	resp, err := s.Send(context.Background(), provider.Input{Text: "read go.mod"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "reading it" {
		t.Fatalf("text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != provider.StopToolUse {
		t.Fatalf("stop = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// Tool result round-trips as a functionResponse naming the called function.
	s.(*session).prov.hc = srv.Client()
	_, err = s.Send(context.Background(), provider.Input{ToolResults: []provider.ToolResult{{ID: "call_1", Content: "module x"}}})
	if err != nil {
		t.Fatal(err)
	}
}
