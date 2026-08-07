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
		// The four above all fit in one screen, so no navigation strategy can
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
