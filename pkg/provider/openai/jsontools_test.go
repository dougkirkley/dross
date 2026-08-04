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
		`{"name":"something_else","arguments":{}}`,          // not a tool we offered
		`{"name":"write"}`,                                  // no arguments
		`{"path":"a","content":"b"}`,                        // no name
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
