package bench

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
			Files: multiFilePkg(),
			Prompt: `Rename the exported function Clamp to Bound everywhere in this package, ` +
				`including its uses and its doc comments. Nothing may still refer to Clamp.`,
			Check: []string{"sh", "-c", "! grep -rq Clamp . && go vet ./..."},
		},
	}
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
