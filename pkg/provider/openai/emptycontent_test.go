package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

// A message with no text must serialise as "" and never be omitted: absent
// content arrives as null, and ollama rejects the whole request with
// "invalid message content type: <nil>".
func TestContentIsAlwaysSent(t *testing.T) {
	for _, m := range []chatMessage{
		{Role: "assistant"},
		{Role: "assistant", ToolCalls: []toolCall{{ID: "1", Type: "function"}}},
		{Role: "tool", ToolCallID: "1"},
	} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"content":""`) {
			t.Errorf("%s message serialised without content: %s", m.Role, b)
		}
		if strings.Contains(string(b), `"content":null`) {
			t.Errorf("%s message sent null content: %s", m.Role, b)
		}
	}
}
