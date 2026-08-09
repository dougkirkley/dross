// Package memory is dross's durable notes: things worth knowing about a
// project that outlive the session that learned them.
//
// It is deliberately not the session brief. A brief is the state of one piece
// of work, written at the end and seeded into the next run of that same work.
// A memory is a standing fact — that this repo's tests need a tag, that the
// staging database is the one behind the VPN, that the user wants commits split
// by concern — and it applies to every session in the project from now on.
//
// Memories are per project. A fact about one checkout is usually wrong about
// another, and a shared pool would leak a client's details into an unrelated
// repository, so each workspace gets its own directory keyed by its absolute
// path.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Budget bounds what is injected into a system prompt.
//
// Memories are supposed to be few and short. The budget is a backstop against
// the failure where a session writes one every turn and eventually crowds the
// prompt out with its own notes — at which point the oldest are dropped and the
// prompt says so, rather than silently carrying a fraction of them.
const Budget = 4000

// Scope is which pool a memory belongs to.
//
// The distinction is what the fact is about. "This repo's tests need the sqlite
// build tag" is about a project and is wrong everywhere else. "Explain the
// tradeoff before writing the code" is about the person, and repeating it in
// every checkout they ever open is how a preference gets forgotten in the one
// they did not.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// Store is one memory directory.
type Store struct{ dir string }

// Set is the pair of pools a session reads: everything the user has learned
// generally, plus everything learned about this workspace.
type Set struct {
	global  *Store
	project *Store
}

// OpenSet opens both pools for a workspace.
func OpenSet(configDir, root string) (*Set, error) {
	project, err := Open(configDir, root)
	if err != nil {
		return nil, err
	}
	global, err := openDir(filepath.Join(configDir, "dross", "memory", "global"))
	if err != nil {
		return nil, err
	}
	return &Set{global: global, project: project}, nil
}

// Store returns the pool for a scope. Anything that is not "global" is the
// project's, so an unset or misspelled scope keeps the narrower blast radius —
// a project note written globally is noise in every other repository, while a
// global note written to a project is merely missed.
func (s *Set) Store(scope Scope) *Store {
	if scope == ScopeGlobal {
		return s.global
	}
	return s.project
}

// List returns every memory in both pools, each labelled with its scope.
func (s *Set) List() []Note {
	out := s.global.listAs(ScopeGlobal)
	return append(out, s.project.listAs(ScopeProject)...)
}

// Prompt renders both pools, general first and project second, so the more
// specific note is the last thing read on any subject the two touch.
//
// Each pool gets its own budget rather than sharing one. A shared budget lets a
// long-lived pile of global notes crowd out the project's, which is exactly
// backwards: the workspace in front of the model is the more relevant of the
// two.
func (s *Set) Prompt() string {
	return s.global.render(ScopeGlobal) + s.project.render(ScopeProject)
}

// nameRE is what a memory may be called: a short kebab-case slug. Restrictive
// on purpose — the name becomes a filename, and a model that can choose
// "../../.ssh/authorized_keys" has been handed a write primitive rather than a
// notebook.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Open returns the memory store for a workspace, creating its directory.
//
// root is keyed by hash as well as basename: two checkouts of the same project
// in different places are different workspaces with different facts, and the
// basename alone would silently merge them.
func Open(configDir, root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(abs))
	slug := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:4])
	return openDir(filepath.Join(configDir, "dross", "memory", slug))
}

func openDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is where this store keeps its files, for a client that wants to say so.
func (s *Store) Dir() string { return s.dir }

// Write saves or replaces a memory. An empty body deletes it, so a model can
// retract something it learned was wrong with the tool it already has.
func (s *Store) Write(name, body string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("memory name %q must be lower-case letters, digits and dashes", name)
	}
	path := filepath.Join(s.dir, name+".md")
	if strings.TrimSpace(body) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600)
}

// Delete removes a memory. Missing is not an error: the caller wanted it gone.
func (s *Store) Delete(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("memory name %q is not valid", name)
	}
	err := os.Remove(filepath.Join(s.dir, name+".md"))
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// Note is one stored memory.
type Note struct {
	Name  string
	Body  string
	Scope Scope
}

// List returns every memory in this pool, by name.
func (s *Store) List() []Note { return s.listAs(ScopeProject) }

func (s *Store) listAs(scope Scope) []Note {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Note
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, Note{
			Name:  strings.TrimSuffix(e.Name(), ".md"),
			Body:  strings.TrimSpace(string(body)),
			Scope: scope,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Prompt renders the memories for a system prompt, or "" when there are none.
//
// Labelled as what it is, and as fallible. A memory was true when it was
// written; the repository has moved since, and a session that trusts a note
// over the file in front of it is worse off than one with no notes at all.
func (s *Store) Prompt() string { return s.render(ScopeProject) }

// render writes one pool's section, headed according to what the notes in it
// are about.
func (s *Store) render(scope Scope) string {
	notes := s.listAs(scope)
	if len(notes) == 0 {
		return ""
	}
	heading := "## Remembered about this project\n\n" +
		"Notes kept from earlier sessions in this workspace."
	if scope == ScopeGlobal {
		heading = "## Remembered generally\n\n" +
			"Notes kept across every project: how this user works, and lessons that were not " +
			"about one repository. Where one of these and a project note disagree, the project " +
			"note is about the code in front of you and wins."
	}
	var b strings.Builder
	b.WriteString("\n\n" + heading + " They were true when they were written and " +
		"nothing has checked them since: if one names a file, a command or a flag, confirm it " +
		"still exists before relying on it. Use the memory tool to correct or remove one that " +
		"turns out to be wrong.\n")
	used, dropped := 0, 0
	for _, n := range notes {
		entry := "\n### " + n.Name + "\n\n" + n.Body + "\n"
		if used+len(entry) > Budget {
			dropped++
			continue
		}
		used += len(entry)
		b.WriteString(entry)
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "\n[%d more memories not shown — the budget is full. "+
			"Prune them with the memory tool.]\n", dropped)
	}
	return b.String()
}
