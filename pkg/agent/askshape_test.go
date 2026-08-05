package agent

import (
	"encoding/json"
	"testing"
)

// A model may answer the schema loosely. A question sent as a bare string is
// unambiguous, and refusing it lost a turn that had already done minutes of
// work.
func TestAskQuestionAcceptsAString(t *testing.T) {
	var in struct {
		Questions []AskQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(`{"questions":["Which file?","Overwrite?"]}`), &in); err != nil {
		t.Fatalf("strings rejected: %v", err)
	}
	if len(in.Questions) != 2 || in.Questions[0].Question != "Which file?" {
		t.Fatalf("got %+v", in.Questions)
	}
	if in.Questions[1].Question != "Overwrite?" {
		t.Errorf("second question = %+v", in.Questions[1])
	}
}

func TestAskQuestionStillAcceptsObjects(t *testing.T) {
	var in struct {
		Questions []AskQuestion `json:"questions"`
	}
	body := `{"questions":[{"header":"h","question":"q","options":["a","b"],"multiSelect":true}]}`
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	q := in.Questions[0]
	if q.Question != "q" || q.Header != "h" || len(q.Options) != 2 || !q.MultiSelect {
		t.Errorf("got %+v", q)
	}
}

// Mixed shapes in one call, since a model that does one may do both.
func TestAskQuestionMixedShapes(t *testing.T) {
	var in struct {
		Questions []AskQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(`{"questions":["plain",{"question":"rich"}]}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Questions[0].Question != "plain" || in.Questions[1].Question != "rich" {
		t.Errorf("got %+v", in.Questions)
	}
}

func TestAskQuestionRejectsNonsense(t *testing.T) {
	var q AskQuestion
	if err := json.Unmarshal([]byte(`42`), &q); err == nil {
		t.Error("a number should not decode as a question")
	}
}
