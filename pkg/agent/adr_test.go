package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedADRs(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The shape of a real one, from k8s-homelab/docs/adr.
const realADR = `# 1. node2 runs a custom Talos kernel for USB/IP

Date: 2026-08-01

## Status

Accepted.

## Context

Steam runs headless on node2 and streams to an NVIDIA Shield.

## Decision

Build a custom Talos kernel with the USB/IP modules:

` + "```" + `
# CONFIG_USBIP_VUDC is not set
CONFIG_USBIP_CORE=m
` + "```" + `

# A later heading that is not the title
`

// TestFindADRsReadsTheRealShape parses what the convention actually produces:
// a numbered filename, a numbered heading, and a Status section.
func TestFindADRsReadsTheRealShape(t *testing.T) {
	root := seedADRs(t, "docs/adr", map[string]string{
		"0001-custom-talos-kernel-for-usbip.md": realADR,
	})
	dir, records := findADRs(root)
	if dir != "docs/adr" {
		t.Fatalf("dir = %q", dir)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	r := records[0]
	if r.num != 1 {
		t.Errorf("num = %d", r.num)
	}
	if !strings.Contains(r.title, "custom Talos kernel") {
		t.Errorf("title = %q", r.title)
	}
	if r.status != "Accepted" {
		t.Errorf("status = %q, want Accepted (trailing full stop trimmed)", r.status)
	}
	if !r.accepted() {
		t.Error("an Accepted record should be binding")
	}
}

// Status decides whether a record constrains anything. Superseded and rejected
// ones are history — treating them as binding would freeze a project to
// decisions it has already moved past.
func TestOnlyAcceptedRecordsBind(t *testing.T) {
	for status, want := range map[string]bool{
		"Accepted": true, "accepted": true, "Accepted, supersedes 2": true,
		"Superseded by 7": false, "Rejected": false, "Proposed": false, "": false,
	} {
		if got := (adr{status: status}).accepted(); got != want {
			t.Errorf("status %q: accepted = %v, want %v", status, got, want)
		}
	}
}

// TestADRGuidanceFramesThemAsConstraints is the point of listing them. Context
// is something a model may weigh against the task; a constraint is not.
func TestADRGuidanceFramesThemAsConstraints(t *testing.T) {
	got := adrGuidance("docs/adr", []adr{
		{num: 1, title: "Custom Talos kernel", status: "Accepted", path: "docs/adr/0001-x.md"},
		{num: 2, title: "Old idea", status: "Superseded by 3", path: "docs/adr/0002-y.md"},
	})
	for _, want := range []string{
		"Custom Talos kernel", "docs/adr/0001-x.md", "Superseded by 3",
		"constraint, not background reading", "say which", "history",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing %q:\n%s", want, got)
		}
	}
	// Nothing to say when a project keeps none.
	if got := adrGuidance("", nil); got != "" {
		t.Errorf("no records should produce no section, got %q", got)
	}
}

// A project with no decision directory gets no section and no tool — dross does
// not impose the convention on repositories that never adopted it.
func TestNoADRDirectoryMeansNoTool(t *testing.T) {
	root := t.TempDir()
	if loc := adrLocation(root); loc != "" {
		t.Fatalf("found a decision directory where there is none: %q", loc)
	}
	for _, d := range toolDefs(false, false, false, false) {
		if d.Name == "decision" {
			t.Fatal("the decision tool should not be offered without somewhere to put one")
		}
	}
	found := false
	for _, d := range toolDefs(false, false, false, true) {
		if d.Name == "decision" {
			found = true
		}
	}
	if !found {
		t.Fatal("a project that keeps records should get the tool")
	}
}

// TestRecordDecisionWritesTheConvention: numbering continues from what is
// there, and the file matches the shape of its neighbours.
func TestRecordDecisionWritesTheConvention(t *testing.T) {
	root := seedADRs(t, "docs/adr", map[string]string{
		"0001-custom-talos-kernel-for-usbip.md": realADR,
	})
	ag, err := New(Config{Name: "t", Model: "m", Workdir: root}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	out, isErr := ag.recordDecision("record", "Ollama spans both GPUs",
		"A 30B model does not fit one 16 GiB card.",
		"Claim both render devices and yield to Sunshine.",
		"ComfyUI has no VRAM while a model is resident.")
	if isErr {
		t.Fatalf("record failed: %s", out)
	}
	want := filepath.Join(root, "docs/adr/0002-ollama-spans-both-gpus.md")
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected %s: %v", want, err)
	}
	text := string(body)
	for _, section := range []string{
		"# 2. Ollama spans both GPUs", "## Status", "Accepted.",
		"## Context", "## Decision", "## Consequences", "Date: ",
	} {
		if !strings.Contains(text, section) {
			t.Errorf("record missing %q:\n%s", section, text)
		}
	}
	// And it is discoverable immediately, which is what makes it worth writing.
	_, records := findADRs(root)
	if len(records) != 2 || records[1].num != 2 {
		t.Fatalf("the new record is not on record: %+v", records)
	}
}

// A record of what without why is the half nobody can use later, so context is
// required rather than optional.
func TestRecordDecisionRefusesTheUselessOnes(t *testing.T) {
	root := seedADRs(t, "docs/adr", map[string]string{"0001-x.md": realADR})
	ag, _ := New(Config{Name: "t", Model: "m", Workdir: root}, &scriptedProvider{})
	for _, tc := range []struct{ title, ctx, dec, want string }{
		{"", "c", "d", "needs a title"},
		{"t", "c", "", "needs a decision"},
		{"t", "", "d", "context"},
	} {
		out, isErr := ag.recordDecision("record", tc.title, tc.ctx, tc.dec, "")
		if !isErr || !strings.Contains(out, tc.want) {
			t.Errorf("%+v: got %q, isErr=%v", tc, out, isErr)
		}
	}
}

// It will not create the directory. A repository that keeps no records has not
// asked for them, and inventing a docs/adr tree because something felt
// architectural is the kind of unasked-for change that makes a tool untrusted.
func TestRecordDecisionWillNotInventTheDirectory(t *testing.T) {
	ag, _ := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	out, isErr := ag.recordDecision("record", "t", "c", "d", "")
	if !isErr || !strings.Contains(out, "keeps no decision records") {
		t.Fatalf("got %q, isErr=%v", out, isErr)
	}
	if loc := adrLocation(ag.cfg.Workdir); loc != "" {
		t.Fatalf("it created %s anyway", loc)
	}
}

func TestADRSlug(t *testing.T) {
	for in, want := range map[string]string{
		"node2 runs a custom Talos kernel for USB/IP": "node2-runs-a-custom-talos-kernel-for-usb-ip",
		"Use PostgreSQL, not MySQL":                   "use-postgresql-not-mysql",
		"  spaced  out  ":                             "spaced-out",
		"!!!":                                         "decision",
	} {
		if got := adrSlug(in); got != want {
			t.Errorf("adrSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFindADRsIgnoresFencedContent is a bug the real file caught and no
// synthetic fixture would have.
//
// k8s-homelab's first ADR quotes a kernel config, so it contains lines like
// "# CONFIG_USBIP_VUDC is not set" inside a fence. The parser took the last
// "# " in the file and had no idea it was inside a code block, so the decision
// listed in every prompt was titled "CONFIG_USBIP_VUDC is not set".
func TestFindADRsIgnoresFencedContent(t *testing.T) {
	root := seedADRs(t, "docs/adr", map[string]string{"0001-x.md": realADR})
	_, records := findADRs(root)
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	if got := records[0].title; !strings.Contains(got, "custom Talos kernel") {
		t.Fatalf("title = %q — a fenced line or a later heading won", got)
	}
	if strings.Contains(records[0].title, "CONFIG_") {
		t.Error("a kernel config line inside a fence was read as the title")
	}
}
