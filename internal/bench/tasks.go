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
	}
}
