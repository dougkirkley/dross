package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/memory"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// memoryEnabled reports whether durable memory is on for this session.
//
// On unless the config says otherwise, following `review`: a feature that only
// works when someone finds the switch is a feature most people never get. The
// switch exists because the memory directory is a file on someone's disk that
// a model writes to, and that is a thing to be able to refuse.
func memoryEnabled(f *config.File) bool {
	if f == nil || f.Memory == nil {
		return true
	}
	return *f.Memory
}

// openMemory returns the store for a workspace, or nil when memory is off or
// the directory cannot be made. Nil is a working session without memory rather
// than an error: nowhere to keep notes is a reason to stop keeping them, not a
// reason to refuse to run.
func openMemory(f *config.File, workdir string) *memory.Set {
	if !memoryEnabled(f) {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	set, err := memory.OpenSet(dir, workdir)
	if err != nil {
		return nil
	}
	return set
}

// memoryToolDef describes the `memory` tool.
//
// The description spends most of its length on what NOT to keep, because the
// failure mode of a memory tool is not forgetting — it is a model that writes
// down the contents of the file it just read, and a prompt that fills with
// restatements of the repository. What earns a memory is what the code cannot
// say: a preference, a constraint, a decision and its reason.
func memoryToolDef() provider.ToolDef {
	return provider.ToolDef{
		Name: "memory",
		Description: "memory: keep a durable note about this project, across sessions. " +
			"op=write saves or replaces one, op=delete removes one, op=list shows what is stored. " +
			"Worth remembering: how the user wants you to work, a constraint or preference they " +
			"stated, why a decision was made, where something external lives. " +
			"Not worth remembering: anything already in the code, the git history, or a project " +
			"file — those can be read, and a note about them goes stale the moment they change. " +
			"Nothing that matters only to the current task. Correct or delete a memory as soon as " +
			"you find it is wrong. " +
			"scope=project (the default) is for facts about this codebase; scope=global is for " +
			"what stays true in every project — how the user wants you to work, and lessons that " +
			"were not about one repository. When in doubt use project: a global note is read in " +
			"every session the user ever starts.",
		Properties: map[string]any{
			"op": map[string]any{"type": "string", "enum": []string{"write", "delete", "list"},
				"description": "write, delete, or list."},
			"name": strMemProp("Short kebab-case identifier, e.g. prefers-table-driven-tests. " +
				"Reusing a name replaces that memory, which is how to correct one."),
			"content": strMemProp("op=write: the note itself, in a sentence or two. " +
				"Include why, not only what."),
			"scope": map[string]any{"type": "string", "enum": []string{"project", "global"},
				"description": "project (default) for this codebase, global for every project."},
		},
		Required: []string{"op"},
	}
}

func strMemProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// memoryToolHandler reads and writes the project's memories.
func memoryToolHandler(set *memory.Set) agent.ToolHandler {
	return func(_ context.Context, input json.RawMessage) (string, bool) {
		if set == nil {
			return "memory is turned off for this session", true
		}
		var in struct {
			Op      string `json:"op"`
			Name    string `json:"name"`
			Content string `json:"content"`
			Scope   string `json:"scope"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return err.Error(), true
		}
		scope := memory.Scope(strings.ToLower(strings.TrimSpace(in.Scope)))
		st := set.Store(scope)
		where := "project"
		if scope == memory.ScopeGlobal {
			where = "globally"
		}
		switch strings.ToLower(strings.TrimSpace(in.Op)) {
		case "list", "":
			// Both pools, always. A model listing memories is deciding whether
			// something is already known, and showing it half the answer is how
			// a duplicate gets written into the other scope.
			notes := set.List()
			if len(notes) == 0 {
				return "no memories stored yet, for this project or globally", false
			}
			var b strings.Builder
			for _, n := range notes {
				fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Scope, n.Name, firstLineOf(n.Body))
			}
			return b.String(), false
		case "write":
			if in.Name == "" {
				return "a memory needs a name", true
			}
			if strings.TrimSpace(in.Content) == "" {
				return "a memory needs content; use op=delete to remove one", true
			}
			if err := st.Write(in.Name, in.Content); err != nil {
				return err.Error(), true
			}
			return fmt.Sprintf("remembered %s (%s)", in.Name, where), false
		case "delete":
			if in.Name == "" {
				return "which memory? pass its name", true
			}
			if err := st.Delete(in.Name); err != nil {
				return err.Error(), true
			}
			return fmt.Sprintf("forgot %s (%s)", in.Name, where), false
		}
		return fmt.Sprintf("unknown op %q; use write, delete or list", in.Op), true
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
