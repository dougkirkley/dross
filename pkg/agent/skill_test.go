package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillTool(t *testing.T) {
	a, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, nil,
		WithSkills([]SkillSpec{
			{Name: "review", Description: "review a diff", Body: "Do the review."},
			{Name: "empty"}, // no body → dropped
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.skills) != 1 {
		t.Fatalf("expected 1 skill (empty dropped), got %d", len(a.skills))
	}

	// The skill tool is advertised, listing the skill in its description.
	var found *string
	for _, d := range a.enabledTools() {
		if d.Name == "skill" {
			found = &d.Description
		}
	}
	if found == nil {
		t.Fatal("skill tool not advertised")
	}
	if !strings.Contains(*found, "review") {
		t.Fatalf("skill tool description missing skill: %q", *found)
	}

	// Loading a skill returns its body.
	out, isErr := a.runSkill(json.RawMessage(`{"name":"review"}`))
	if isErr || out != "Do the review." {
		t.Fatalf("runSkill = %q (err=%v)", out, isErr)
	}
	// Unknown skill errors.
	if _, isErr := a.runSkill(json.RawMessage(`{"name":"nope"}`)); !isErr {
		t.Fatal("unknown skill should error")
	}
}
