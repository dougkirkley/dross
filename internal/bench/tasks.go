package bench

import (
	"fmt"
	"strings"
)

// DefaultTasks are small, objectively-checkable coding tasks. Each seeds a fresh
// Go workspace, gives a prompt, and grades with a shell check that must exit 0.
func DefaultTasks() []Task {
	return []Task{
		{
			Name: "edit-output",
			Files: map[string]string{
				"go.mod":  "module bench\n\ngo 1.21\n",
				"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
			},
			Prompt: `Change main.go so the program prints "goodbye" instead of "hello". Make sure it still builds.`,
			Check:  []string{"sh", "-c", "go build ./... && go run . | grep -qx goodbye"},
		},
		{
			Name: "add-function",
			Files: map[string]string{
				"go.mod":       "module bench\n\ngo 1.21\n",
				"math.go":      "package bench\n",
				"math_test.go": "package bench\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"Add(2,3) != 5\")\n\t}\n}\n",
			},
			Prompt: `Add an exported function Add(a, b int) int to math.go that returns a+b, so that go test passes.`,
			Check:  []string{"go", "test", "./..."},
		},
		// The two above are one-shot edits: they finish in a turn or two and so
		// measure little but model latency. The two below are the ones with
		// something to say about the agent — they can't be done without reading
		// several files first, which is where a turn's wall-clock is won or
		// lost.
		{
			Name:   "find-the-bug",
			Files:  multiFilePkg(),
			Prompt: `go test ./... fails. Find the bug and fix it. Do not change the test.`,
			Check:  []string{"go", "test", "./..."},
		},
		{
			Name:  "rename-symbol",
			Files: workingPkg(),
			Prompt: `Rename the exported function Clamp to Bound everywhere in this package, ` +
				`including its uses and its doc comments. Nothing may still refer to Clamp.`,
			Check: []string{"sh", "-c", "! grep -rq Clamp . && go test ./..."},
		},
		// The two above are written the way a task is written, not the way a
		// person talks. Real prompts arrive lowercase and unpunctuated, describe
		// a symptom rather than name a file, and never say "run the tests" —
		// they assume you know that. The two below are the same work asked the
		// way it actually gets asked, so the pair isolates prompt shape from
		// difficulty: a runner that scores well on one and badly on its twin is
		// being beaten by the wording, not by the code.
		{
			Name:  "report-a-bug",
			Files: multiFilePkg(),
			Prompt: `the limiter hands back the top of the range for anything under the bottom of it ` +
				`— 0 with the default config comes out as 10 when it should be 1. can you sort that out`,
			Check: []string{"go", "test", "./..."},
		},
		{
			Name:   "ask-for-a-feature",
			Files:  askPkg(),
			Verify: askAcceptance(),
			Prompt: `the picker only lets you pick one thing. let people pick more than one, ` +
				`comma separated, and if they type something that isn't on the list just take that ` +
				`as their answer`,
			// -timeout guards the likeliest wrong turn: a read loop that no
			// longer terminates leaves go test wedged for ten minutes and eats
			// the whole task budget rather than failing.
			Check: []string{"go", "test", "-timeout", "60s", "./..."},
		},
		{
			Name:   "two-part-request",
			Files:  workingPkg(),
			Verify: twoPartAcceptance(),
			Prompt: `couple of things — Config needs a String method so it prints like "lo..hi", ` +
				`and something to tell me whether a config is sane, i.e. its low bound isn't above its high one`,
			// Two independent edits in one sentence, which is how requests
			// actually arrive. Half-doing it is the failure being watched for:
			// the check fails if either half is missing, and neither half
			// depends on the other, so there is no order to get wrong.
			//
			// Both halves are additions. The first draft asked for the default
			// low bound to move to 0, which limiter_test.go already pins at 1 —
			// a request the seed contradicts, so an agent would have been graded
			// on which of two conflicting instructions it guessed at. That is
			// the seed-does-not-match-the-task mistake, and it invalidates the
			// measurement rather than making it harder.
			Check: []string{"go", "test", "-timeout", "60s", "./..."},
		},
		// The seven above all fit in one screen, so no navigation strategy can
		// distinguish itself: reading every file is a perfectly good plan. This
		// one is forty-odd files across fourteen packages, where it is not.
		{
			Name:  "find-in-a-tree",
			Files: bigTree(),
			Prompt: `go test ./... fails. Exactly one component's Rate() disagrees with the rate ` +
				`documented in its own package comment. Find it and correct the code to match the ` +
				`documentation. Do not change any documentation or any test.`,
			Check: []string{"go", "test", "./..."},
		},
		// The tasks above all fit in files small enough to read whole. This one
		// does not: its answer is in a 300 KB log, which read refuses to hand
		// over in one piece. What is being watched is what the run does about
		// that — tail it with a slice, grep it for the word that matters, or
		// discover the refusal and go round it with bash. All three are fine
		// answers; flailing at the refusal is not, and that is the failure this
		// exists to catch.
		{
			Name:   "read-a-big-log",
			Files:  bigLogPkg(),
			Verify: bigLogAcceptance(),
			Prompt: `last night's run died and left run.log behind. work out what it was ` +
				`complaining about and change the code so it wouldn't happen again`,
			Check: []string{"go", "test", "-timeout", "60s", "./..."},
		},
		{
			Name:  "hunt-in-a-tree",
			Files: bigTree(),
			Prompt: `one of these components is reporting a rate that doesn't match what its own ` +
				`docs say. can you track down which one and make the code agree with the docs`,
			// find-in-a-tree's twin, asked the way it would actually be asked:
			// no mention of the failing test, no "exactly one", no instruction
			// about what not to change. This is the pair that exercises search
			// rather than reading — forty files, and the prompt names none of
			// them — so it is where a weak grep shows up as a wrong answer.
			Check: []string{"go", "test", "./..."},
		},
	}
}

// twoPartAcceptance grades both halves of the two-part request separately, so a
// run that did one of them scores zero rather than half.
//
// Written as one file the agent never sees, for the same reason as
// askAcceptance: a request with the test attached is a specification.
func twoPartAcceptance() map[string]string {
	return map[string]string{
		"twopart_test.go": `package bench

import "testing"

func TestConfigStringed(t *testing.T) {
	if got := (Config{Lo: 2, Hi: 9}).String(); got != "2..9" {
		t.Fatalf("String() = %q, want \"2..9\"", got)
	}
}

// The second half. Only the two answers the request actually determines are
// checked — a sane config and an inverted one — and the method may be called
// anything, so long as exactly one bool method exists to call. What it is
// named is the agent's choice, and grading a choice the prompt left open would
// be grading a guess.
func TestConfigSanityReported(t *testing.T) {
	if !configSane(Config{Lo: 1, Hi: 10}) {
		t.Fatal("a config with lo below hi should report sane")
	}
	if configSane(Config{Lo: 10, Hi: 1}) {
		t.Fatal("a config with lo above hi should not report sane")
	}
}
`,
		// The name the agent chose, found rather than guessed at.
		//
		// This was a list of the names it might have picked — Valid, IsValid,
		// Sane, OK — and the first real run wrote IsSane and was marked wrong
		// for it. The method was correct, the request did not name it, and the
		// grader failed an answer for spelling. Any list will be missing the
		// fifth name, so there is no list: the prompt asked for something that
		// reports whether a config is sane, and a niladic bool method on Config
		// is exactly that, whatever it is called.
		"twopart_shim_test.go": `package bench

import "reflect"

// configSane calls whatever niladic bool method the agent added to Config.
func configSane(c Config) bool {
	for _, v := range []reflect.Value{reflect.ValueOf(c), reflect.ValueOf(&c)} {
		t := v.Type()
		for i := 0; i < t.NumMethod(); i++ {
			mt := t.Method(i).Type
			if mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0).Kind() == reflect.Bool {
				return v.Method(i).Call(nil)[0].Bool()
			}
		}
	}
	panic("Config has no bool method reporting whether it is sane")
}
`,
	}
}

// logBytes is how big run.log is made. Over the 256 KB a single read returns,
// so the file cannot be taken in one piece and the run has to decide how to
// approach it — which is the whole point of the task.
const logBytes = 300 * 1024

// bigLogPkg seeds a small, working package next to the log that says what is
// wrong with it.
//
// The code is green and stays green: the seed's own test only checks that the
// knobs are positive, so nothing in the workspace states the answer. The answer
// is one line near the end of a file too long to read — which is the shape of
// the problem this task is for, and the reason the interesting part is not the
// edit but the navigation.
func bigLogPkg() map[string]string {
	return map[string]string{
		"go.mod": "module bench\n\ngo 1.21\n",
		"retry.go": "// Package bench retries requests against a backend.\npackage bench\n\n" +
			"// MaxRetries is how many times a request is tried before it is given up on.\n" +
			"func MaxRetries() int {\n\treturn 3\n}\n\n" +
			"// Backoff is the pause between attempts, in milliseconds.\n" +
			"func Backoff() int {\n\treturn 250\n}\n",
		"retry_test.go": "package bench\n\nimport \"testing\"\n\n" +
			"func TestKnobsArePositive(t *testing.T) {\n" +
			"\tif MaxRetries() <= 0 {\n\t\tt.Fatal(\"retries must be positive\")\n\t}\n" +
			"\tif Backoff() <= 0 {\n\t\tt.Fatal(\"backoff must be positive\")\n\t}\n}\n",
		"run.log": bigLog(),
	}
}

// bigLog is a night of uneventful logging with the reason for the crash in the
// last few lines.
//
// Boring on purpose. A log whose every line is interesting can be sampled
// anywhere; this one answers only at the end, so a run that reads the first
// page and stops has read nothing. The FATAL line names the number it wants,
// because a task graded on a number the log never states is graded on a guess.
func bigLog() string {
	var b strings.Builder
	for i := 1; b.Len() < logBytes; i++ {
		fmt.Fprintf(&b, "2026-08-12T03:%02d:%02dZ INFO  request %d ok in %dms (attempt 1/3, backend fast-path)\n",
			(i/60)%60, i%60, i, 20+i%80)
		if i%97 == 0 {
			fmt.Fprintf(&b, "2026-08-12T03:%02d:%02dZ WARN  request %d retried once and recovered\n",
				(i/60)%60, i%60, i)
		}
	}
	b.WriteString(`2026-08-12T04:11:57Z ERROR request 90210 failed (attempt 1/3): slow backend timed out after 2000ms
2026-08-12T04:11:57Z ERROR request 90210 failed (attempt 2/3): slow backend timed out after 2000ms
2026-08-12T04:11:58Z ERROR request 90210 failed (attempt 3/3): slow backend timed out after 2000ms
2026-08-12T04:11:58Z FATAL giving up on request 90210: the slow backend needs a retry budget of at least 5 attempts, and MaxRetries is 3
2026-08-12T04:11:58Z INFO  draining in-flight requests
2026-08-12T04:11:58Z INFO  shutdown complete
`)
	return b.String()
}

// bigLogAcceptance grades the one thing the log determines and nothing else.
//
// Written after the run, like askAcceptance: a visible test would name the
// number and turn the log into decoration. How the retry loop is arranged, and
// whether the backoff moved with it, is the agent's business — the log asked
// for a budget of at least five, and that is what is checked.
func bigLogAcceptance() map[string]string {
	return map[string]string{
		"acceptance_test.go": `package bench

import "testing"

func TestRetryBudgetRaised(t *testing.T) {
	if got := MaxRetries(); got < 5 {
		t.Fatalf("MaxRetries() = %d; the log asked for a budget of at least 5", got)
	}
}
`,
	}
}

// treeParts are the components of the generated tree. Names rather than
// numbers, so a search for one of them means something and the model cannot
// navigate by arithmetic.
var treeParts = []string{
	"ledger", "billing", "shipping", "catalog", "pricing", "inventory", "payments",
	"accounts", "sessions", "reporting", "shipping2", "taxes", "refunds", "audit",
}

// brokenPart is the component whose code disagrees with its own documentation,
// and by how much. Far enough down the list that a model working alphabetically
// does not find it first by luck.
const (
	brokenPart = "reporting"
	brokenBy   = 7
)

// bigTree seeds a package per component, each with its rate stated in its own
// doc comment and returned by its own function, and a registry that sums them.
//
// One component's code disagrees with its documentation. Nothing else does, the
// component's own test still passes — it only checks the rate is positive — and
// the failure surfaces two packages away in the registry total. So the task
// cannot be done by reading the failing test, and reading all forty files to
// find it is the expensive way: the cheap way is to search for the thing that
// is stated twice and compare.
func bigTree() map[string]string {
	files := map[string]string{"go.mod": "module bench\n\ngo 1.21\n"}
	total := 0
	var imports, sum strings.Builder
	for i, name := range treeParts {
		rate := (i + 1) * 10
		total += rate
		got := rate
		if name == brokenPart {
			got = rate + brokenBy
		}
		dir := "internal/" + name + "/"
		files[dir+name+".go"] = fmt.Sprintf(
			"// Package %s reports the %s component's contribution.\n"+
				"//\n"+
				"// Rate is %d.\n"+
				"package %s\n\n"+
				"// Rate returns this component's fixed contribution to the total.\n"+
				"func Rate() int {\n\treturn %d\n}\n",
			name, name, rate, name, got)
		files[dir+"doc.go"] = fmt.Sprintf(
			"package %s\n\n// Name identifies this component in the registry.\nfunc Name() string {\n\treturn %q\n}\n",
			name, name)
		files[dir+name+"_test.go"] = fmt.Sprintf(
			"package %s\n\nimport \"testing\"\n\n"+
				"func TestRateIsPositive(t *testing.T) {\n\tif Rate() <= 0 {\n\t\tt.Fatal(\"rate must be positive\")\n\t}\n}\n\n"+
				"func TestName(t *testing.T) {\n\tif Name() != %q {\n\t\tt.Fatal(\"wrong name\")\n\t}\n}\n",
			name, name)
		fmt.Fprintf(&imports, "\t\"bench/internal/%s\"\n", name)
		fmt.Fprintf(&sum, "\t\t%s.Rate() +\n", name)
	}

	files["registry.go"] = "// Package bench totals every component's rate.\npackage bench\n\nimport (\n" +
		imports.String() + ")\n\n" +
		"// Total is the sum of every component's documented rate.\nfunc Total() int {\n\treturn 0 +\n" +
		sum.String() + "\t\t0\n}\n"
	files["registry_test.go"] = fmt.Sprintf(
		"package bench\n\nimport \"testing\"\n\n"+
			"// The expected total is the sum of the rates the packages document.\n"+
			"func TestTotal(t *testing.T) {\n\tif got := Total(); got != %d {\n"+
			"\t\tt.Fatalf(\"Total() = %%d, want %%d\", got, %d)\n\t}\n}\n",
		total, total)
	return files
}

// askPkg seeds a one-answer picker: a question, numbered options, and a caller
// that prints what came back. Nothing in it is broken.
//
// The task on top of it is a feature request in the shape people write them —
// no file named, no API given, no test mentioned. What keeps that gradable is
// that the shape of the answer is already pinned by the code: Ask returns a
// slice, its one caller joins that slice, and the seed's own test fixes the
// signature. So "let people pick more than one" has exactly one sensible
// landing place, and askAcceptance can check it without having guessed at
// names the agent was free to choose.
func askPkg() map[string]string {
	return map[string]string{
		"go.mod": "module bench\n\ngo 1.21\n",
		"ask.go": `// Package ask puts a question to the person at the terminal.
package ask

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Question is one thing to ask.
type Question struct {
	Text    string
	Options []string
}

// Ask prints q and reads the reply, returning what the user chose.
//
// One option, given by its number. Anything else is not an answer, so the
// question goes again — until it gets one, or the input runs out.
func Ask(in io.Reader, out io.Writer, q Question) []string {
	lines := bufio.NewScanner(in)
	for {
		fmt.Fprintln(out, q.Text)
		for i, opt := range q.Options {
			fmt.Fprintf(out, "  %d) %s\n", i+1, opt)
		}
		if !lines.Scan() {
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(lines.Text()))
		if err != nil || n < 1 || n > len(q.Options) {
			fmt.Fprintln(out, "pick one of the numbers")
			continue
		}
		return []string{q.Options[n-1]}
	}
}
`,
		"report.go": `package ask

import "strings"

// Answer renders what Ask returned, for a transcript the user can read back.
func Answer(picked []string) string {
	if len(picked) == 0 {
		return "(no answer)"
	}
	return strings.Join(picked, ", ")
}
`,
		"ask_test.go": `package ask

import (
	"io"
	"strings"
	"testing"
)

func TestPicksByNumber(t *testing.T) {
	got := Ask(strings.NewReader("2\n"), io.Discard, Question{
		Text:    "which one",
		Options: []string{"alpha", "beta", "gamma"},
	})
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("Ask = %v, want [beta]", got)
	}
}

func TestAnswerJoins(t *testing.T) {
	if got := Answer([]string{"alpha", "gamma"}); got != "alpha, gamma" {
		t.Fatalf("Answer = %q", got)
	}
}
`,
	}
}

// askAcceptance is the test the agent never sees, written into the workspace
// once it has stopped. It grades the three things the prompt asked for and
// nothing else — one pick still works, several picks work, and something that
// is not on the list is taken at its word.
//
// Deliberately not in the seed. A visible acceptance test turns a request into
// a specification, and every agent then scores the same: read the test, satisfy
// the test. What is being measured here is what happens without one.
func askAcceptance() map[string]string {
	return map[string]string{
		"acceptance_test.go": `package ask

import (
	"io"
	"strings"
	"testing"
)

func ask(input string, options ...string) []string {
	return Ask(strings.NewReader(input), io.Discard, Question{Text: "which one", Options: options})
}

func eq(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestStillTakesOne(t *testing.T) {
	if got := ask("2\n", "alpha", "beta", "gamma"); !eq(got, []string{"beta"}) {
		t.Fatalf("one pick: got %v, want [beta]", got)
	}
}

func TestTakesSeveral(t *testing.T) {
	if got := ask("1,3\n", "alpha", "beta", "gamma"); !eq(got, []string{"alpha", "gamma"}) {
		t.Fatalf("several picks: got %v, want [alpha gamma]", got)
	}
}

// The write-in carries no comma on purpose. "comma separated" is the rule the
// prompt gave, so a write-in containing one is a question the task never
// answered, and grading an agent on it would be grading a coin toss.
func TestTakesAWriteIn(t *testing.T) {
	if got := ask("neither of those\n", "alpha", "beta"); !eq(got, []string{"neither of those"}) {
		t.Fatalf("write-in: got %v, want [neither of those]", got)
	}
}
`,
	}
}

// workingPkg is multiFilePkg with the bug fixed: the same shape, spread over
// the same files, but its test passes.
//
// This task used to share the broken seed, and it made the benchmark measure
// the wrong thing entirely. The agent is told to verify its work by running the
// tests, so it did — found a failure it had not caused, reasonably assumed it
// had broken something, and spent nine of seventeen turns fixing a bug nobody
// asked it to touch. The rename itself was finished by turn seven.
//
// The check runs the tests now rather than go vet, because with a seed that
// starts green a rename that breaks them is a rename that failed.
func workingPkg() map[string]string {
	files := multiFilePkg()
	files["util.go"] = "package bench\n\n" +
		"// Clamp returns v limited to the range [lo, hi].\nfunc Clamp(v, lo, hi int) int {\n" +
		"\tif v > hi {\n\t\treturn hi\n\t}\n\tif v < lo {\n\t\treturn lo\n\t}\n\treturn v\n}\n"
	return files
}

// multiFilePkg seeds a small package whose bug — Clamp returning hi for a value
// already below lo — is three files away from the test that catches it. Small
// enough to be cheap, spread out enough that it cannot be solved without
// looking around first.
func multiFilePkg() map[string]string {
	return map[string]string{
		"go.mod": "module bench\n\ngo 1.21\n",
		"config.go": "package bench\n\n" +
			"// Config bounds what the limiter will accept.\ntype Config struct {\n\tLo, Hi int\n}\n\n" +
			"// DefaultConfig is the configuration used when none is given.\nfunc DefaultConfig() Config {\n\treturn Config{Lo: 1, Hi: 10}\n}\n",
		"util.go": "package bench\n\n" +
			"// Clamp returns v limited to the range [lo, hi].\nfunc Clamp(v, lo, hi int) int {\n" +
			"\tif v > hi {\n\t\treturn hi\n\t}\n\tif v < lo {\n\t\treturn hi\n\t}\n\treturn v\n}\n",
		"limiter.go": "package bench\n\n" +
			"// Limiter applies a Config to incoming values.\ntype Limiter struct {\n\tcfg Config\n}\n\n" +
			"// NewLimiter builds a Limiter from cfg.\nfunc NewLimiter(cfg Config) *Limiter {\n\treturn &Limiter{cfg: cfg}\n}\n\n" +
			"// Apply clamps v into the limiter's configured range.\nfunc (l *Limiter) Apply(v int) int {\n\treturn Clamp(v, l.cfg.Lo, l.cfg.Hi)\n}\n",
		"limiter_test.go": "package bench\n\nimport \"testing\"\n\n" +
			"func TestApply(t *testing.T) {\n\tl := NewLimiter(DefaultConfig())\n" +
			"\tfor _, tc := range []struct{ in, want int }{\n" +
			"\t\t{0, 1},\n\t\t{5, 5},\n\t\t{99, 10},\n\t} {\n" +
			"\t\tif got := l.Apply(tc.in); got != tc.want {\n" +
			"\t\t\tt.Errorf(\"Apply(%d) = %d, want %d\", tc.in, got, tc.want)\n\t\t}\n\t}\n}\n",
	}
}
