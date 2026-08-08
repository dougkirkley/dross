package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// grepLike and readLike mirror the real tool schemas closely enough to test the
// types that were arriving wrong: an int, a bool and an array of strings.
var typedTools = []provider.ToolDef{
	{
		Name: "grep",
		Properties: map[string]any{
			"pattern":    map[string]any{"type": "string"},
			"context":    map[string]any{"type": "integer"},
			"files_only": map[string]any{"type": "boolean"},
		},
	},
	{
		Name: "read",
		Properties: map[string]any{
			"path":  map[string]any{"type": "string"},
			"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	},
}

func argsOf(t *testing.T, xml string) map[string]any {
	t.Helper()
	calls, _ := parseCalls(xml, typedTools)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	var out map[string]any
	if err := json.Unmarshal(calls[0].Input, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTypedArgumentsSurviveTheProtocol is the fix. The XML carries no types —
// every value is the text between two tags — and sending them all on as strings
// put "3" in front of a field declared int. encoding/json fails the WHOLE
// object on the first bad field, so the grep lost its pattern too and came back
// as an unmarshal error; the model retried the same call, then fell back to
// reading the file through bash. Two turns for a value that was never in doubt.
func TestTypedArgumentsSurviveTheProtocol(t *testing.T) {
	args := argsOf(t, "<function=grep>\n<parameter=pattern>Clamp</parameter>\n"+
		"<parameter=context>3</parameter>\n<parameter=files_only>true</parameter>\n</function>")

	if got, ok := args["context"].(float64); !ok || got != 3 {
		t.Errorf("context = %#v, want the number 3", args["context"])
	}
	if got, ok := args["files_only"].(bool); !ok || !got {
		t.Errorf("files_only = %#v, want true", args["files_only"])
	}
	if got, ok := args["pattern"].(string); !ok || got != "Clamp" {
		t.Errorf("pattern = %#v, want the string \"Clamp\"", args["pattern"])
	}

	// The whole point: what the agent unmarshals this into now succeeds.
	calls, _ := parseCalls("<function=grep>\n<parameter=pattern>Clamp</parameter>\n"+
		"<parameter=context>3</parameter>\n</function>", typedTools)
	var in struct {
		Pattern string `json:"pattern"`
		Context int    `json:"context"`
	}
	if err := json.Unmarshal(calls[0].Input, &in); err != nil {
		t.Fatalf("the agent could not read its own tool call: %v", err)
	}
	if in.Pattern != "Clamp" || in.Context != 3 {
		t.Errorf("in = %+v", in)
	}
}

// An array parameter is how one read call covers several files, which is the
// batching that cut turns in the first place. It arrived as a string holding
// JSON, so the batch silently read nothing.
func TestArrayArgumentsArriveAsArrays(t *testing.T) {
	args := argsOf(t, `<function=read>`+"\n"+
		`<parameter=paths>["util.go", "limiter.go"]</parameter>`+"\n</function>")
	got, ok := args["paths"].([]any)
	if !ok {
		t.Fatalf("paths = %#v, want an array", args["paths"])
	}
	if len(got) != 2 || got[0] != "util.go" || got[1] != "limiter.go" {
		t.Errorf("paths = %#v", got)
	}
}

// One per line is the other way a model writes a list into a text field, and a
// lone value is a list of one. Both beat handing an array-typed field a string.
func TestArrayAcceptsTheOtherWaysModelsWriteLists(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"lines", "util.go\nlimiter.go", []string{"util.go", "limiter.go"}},
		{"one value", "util.go", []string{"util.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := argsOf(t, "<function=read>\n<parameter=paths>"+tc.body+"</parameter>\n</function>")
			got, ok := args["paths"].([]any)
			if !ok || len(got) != len(tc.want) {
				t.Fatalf("paths = %#v, want %v", args["paths"], tc.want)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("paths[%d] = %v, want %v", i, got[i], w)
				}
			}
		})
	}
}

// A value that cannot be read as its declared type is passed through as what it
// was, so the tool reports something the model can act on rather than this
// quietly inventing a zero and searching for the wrong thing.
func TestUnreadableValuesArePassedThroughNotGuessed(t *testing.T) {
	args := argsOf(t, "<function=grep>\n<parameter=pattern>x</parameter>\n"+
		"<parameter=context>lots</parameter>\n</function>")
	if got, ok := args["context"].(string); !ok || got != "lots" {
		t.Errorf("context = %#v, want the string it arrived as", args["context"])
	}
}

// Unknown tools and untyped schemas both mean "string", which is what every
// argument was before this existed.
func TestUnknownToolsKeepTheOldBehaviour(t *testing.T) {
	calls, _ := parseCalls("<function=mystery>\n<parameter=n>3</parameter>\n</function>", typedTools)
	var out map[string]any
	if err := json.Unmarshal(calls[0].Input, &out); err != nil {
		t.Fatal(err)
	}
	if got, ok := out["n"].(string); !ok || got != "3" {
		t.Errorf("n = %#v, want the string \"3\"", out["n"])
	}
}

// Whitespace is part of the value for code, and must not be coerced away — the
// reason paramValue exists. A typed field trims, an untyped one does not.
func TestCoercionDoesNotDisturbVerbatimValues(t *testing.T) {
	calls, _ := parseCalls("<function=edit>\n<parameter=old_str>\tif v > hi {</parameter>\n</function>",
		typedTools)
	var out map[string]string
	if err := json.Unmarshal(calls[0].Input, &out); err != nil {
		t.Fatal(err)
	}
	if out["old_str"] != "\tif v > hi {" {
		t.Errorf("old_str = %q, want its leading tab", out["old_str"])
	}
}

// A driven reply with no tool calls normally means the task is done — that is
// how the model says so. Roughly one turn in fifteen it means the opposite: the
// call was emitted with a malformed opening tag, nothing parsed, and reporting
// a normal end of turn ended the run with the file still empty. Measured on
// add-function: one turn, 2.5s, recorded as a pass-rate failure with no error
// to explain it.
func TestBrokenCallIsNotMistakenForFinishing(t *testing.T) {
	// The shape actually observed: enough of the tag survived to be stripped,
	// not enough to parse, leaving the arguments as bare text.
	broken := "=read>\n[\"math.go\", \"math_test.go\", \"go.mod\"]"
	if !attemptedCall(broken) {
		t.Errorf("an orphaned opening tag should read as an attempted call:\n%s", broken)
	}
	for _, s := range []string{
		"<function=read>\n<parameter=path>go.mod",    // never closed
		"I will now <invoke name=\"read\"> the file", // half-normalized
		"<parameter=path>go.mod</parameter>",         // parameters, no function
	} {
		if !attemptedCall(s) {
			t.Errorf("scaffolding should read as an attempted call: %q", s)
		}
	}
}

// The other half has to keep working, or every finished task becomes an error:
// a plain prose answer is how a driven model reports it is done.
func TestPlainAnswersStillFinish(t *testing.T) {
	for _, s := range []string{
		"Added `Add(a, b int) int` to math.go; `go test ./...` passes.",
		"Done — the rename is complete and nothing refers to Clamp.",
		"I read the file and the rate is 0.05, which matches the documentation.",
		"", // an empty reply is the agent's own nudge to handle, not a broken call
	} {
		if attemptedCall(s) {
			t.Errorf("a finished answer should not read as a broken call: %q", s)
		}
	}
}

// The error has to be the one the agent repairs, not a generic failure, or the
// run ends on it instead of asking again.
func TestBrokenCallReportsTheRepairableError(t *testing.T) {
	if !errors.Is(fmt.Errorf("%w: %s", provider.ErrMalformedToolCall, "x"), provider.ErrMalformedToolCall) {
		t.Fatal("the wrapping must keep errors.Is working")
	}
}
