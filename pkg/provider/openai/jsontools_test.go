package openai

import "testing"

func withTools(names ...string) *session {
	s := &session{}
	for _, n := range names {
		var t toolSpec
		t.Type = "function"
		t.Function.Name = n
		s.tools = append(s.tools, t)
	}
	return s
}

// Exactly what qwen2.5-coder:14b returned: the right call, in the wrong field.
func TestParseJSONToolCalls(t *testing.T) {
	s := withTools("write", "bash")
	calls, rest := s.parseJSONToolCalls(
		`{"name":"write","arguments":{"path":"hello.go","content":"package main"}}`)
	if len(calls) != 1 || calls[0].Function.Name != "write" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Function.Arguments != `{"path":"hello.go","content":"package main"}` {
		t.Errorf("arguments = %s", calls[0].Function.Arguments)
	}
	if rest != "" {
		t.Errorf("leftover text = %q, want none", rest)
	}
}

func TestParseJSONToolCallsFenced(t *testing.T) {
	s := withTools("bash")
	calls, _ := s.parseJSONToolCalls("```json\n{\"name\":\"bash\",\"arguments\":{\"command\":\"go test\"}}\n```")
	if len(calls) != 1 || calls[0].Function.Name != "bash" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestParseJSONToolCallsArray(t *testing.T) {
	s := withTools("write")
	calls, _ := s.parseJSONToolCalls(
		`[{"name":"write","arguments":{"path":"a"}},{"name":"write","arguments":{"path":"b"}}]`)
	if len(calls) != 2 || calls[0].ID == calls[1].ID {
		t.Fatalf("calls = %+v", calls)
	}
}

// The guard: an answer that merely contains JSON is not a tool call. Getting
// this wrong would eat a legitimate reply and run something instead.
func TestParseJSONToolCallsLeavesProseAlone(t *testing.T) {
	s := withTools("write", "bash")
	for _, in := range []string{
		`{"name":"something_else","arguments":{}}`,           // not a tool we offered
		`{"name":"write"}`,                                   // no arguments
		`{"path":"a","content":"b"}`,                         // no name
		`Here is some JSON: {"name":"write","arguments":{}}`, // prose around it
		"plain prose",
		"",
	} {
		if calls, rest := s.parseJSONToolCalls(in); len(calls) != 0 || rest != in {
			t.Errorf("parseJSONToolCalls(%q) = %+v, %q — want it untouched", in, calls, rest)
		}
	}

	// A session with no tools can never be answering with one.
	if calls, _ := withTools().parseJSONToolCalls(`{"name":"write","arguments":{"path":"a"}}`); len(calls) != 0 {
		t.Error("a session offering no tools must not synthesize a call")
	}
}

// withParams builds a session whose tools have real parameter schemas, which is
// what an arguments-only object has to be matched against.
func withParams(specs map[string][]string, req map[string][]string) *session {
	s := &session{}
	for name, props := range specs {
		var t toolSpec
		t.Type = "function"
		t.Function.Name = name
		p := map[string]any{}
		for _, k := range props {
			p[k] = map[string]any{"type": "string"}
		}
		params := map[string]any{"type": "object", "properties": p}
		if r := req[name]; len(r) > 0 {
			any_ := make([]any, 0, len(r))
			for _, k := range r {
				any_ = append(any_, k)
			}
			params["required"] = any_
		}
		t.Function.Parameters = params
		s.tools = append(s.tools, t)
	}
	return s
}

// Exactly what gpt-oss:20b returned: the arguments object on its own, with no
// name on it. The turn ended having announced work it never did, and the raw
// object was rendered as the answer.
func TestArgumentsOnlyCallIsRecovered(t *testing.T) {
	s := withParams(
		map[string][]string{
			"bash": {"command", "background"},
			"read": {"path"},
		},
		map[string][]string{"bash": {"command"}, "read": {"path"}},
	)

	calls, rest := s.parseJSONToolCalls(`{"background":false,"command":"which kubebuilder"}`)
	if len(calls) != 1 || calls[0].Function.Name != "bash" {
		t.Fatalf("calls = %+v", calls)
	}
	if rest != "" {
		t.Errorf("left prose behind: %q", rest)
	}
}

// The name is inferred from the shape, so the shape has to mean one thing.
func TestArgumentsOnlyRefusesToGuess(t *testing.T) {
	// Two tools would take {"path": ...}. Guessing which is worse than prose.
	s := withParams(
		map[string][]string{"read": {"path"}, "delete": {"path"}},
		map[string][]string{"read": {"path"}, "delete": {"path"}},
	)
	if calls, rest := s.parseJSONToolCalls(`{"path":"go.mod"}`); calls != nil || rest == "" {
		t.Errorf("guessed between two tools: %+v", calls)
	}

	// An ordinary JSON answer names nothing we offer.
	s2 := withParams(map[string][]string{"bash": {"command"}}, map[string][]string{"bash": {"command"}})
	if calls, _ := s2.parseJSONToolCalls(`{"name":"Ada","age":36}`); calls != nil {
		t.Errorf("read an answer as a call: %+v", calls)
	}

	// Missing a required property is not that tool's call.
	if calls, _ := s2.parseJSONToolCalls(`{"command":""}`); len(calls) != 1 {
		t.Errorf("a present-but-empty required property is still present: %+v", calls)
	}
	s3 := withParams(map[string][]string{"bash": {"command", "background"}},
		map[string][]string{"bash": {"command"}})
	if calls, _ := s3.parseJSONToolCalls(`{"background":true}`); calls != nil {
		t.Errorf("accepted a call missing its required property: %+v", calls)
	}
}
