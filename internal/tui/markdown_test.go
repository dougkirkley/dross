package tui

import "testing"

func TestHoistMarker(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[Present and listening]\n\nBound and responsive.", "[Present and listening] Bound and responsive."},
		{"[Analyzing] already has a headline.\n\nmore", "[Analyzing] already has a headline.\n\nmore"},
		{"[Plan]\n\n- step one\n- step two", "[Plan]\n\n- step one\n- step two"}, // don't fold a list
		{"no marker here\nsecond line", "no marker here\nsecond line"},
		{"[Done]\ncode below\n```go\nx\n```", "[Done] code below\n```go\nx\n```"},
	}
	for _, c := range cases {
		if got := hoistMarker(c.in); got != c.want {
			t.Errorf("hoistMarker(%q)\n got: %q\nwant: %q", c.in, got, c.want)
		}
	}
}

func TestTagCodeFences(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "filename as info string",
			in:   "```main.go\npackage main\n```",
			want: "```go\npackage main\n```",
		},
		{
			name: "path as info string",
			in:   "```cmd/dross/main.go\npackage main\n```",
			want: "```go\npackage main\n```",
		},
		{
			name: "infer from preceding line",
			in:   "See config.py:\n```\nx = 1\n```",
			want: "See config.py:\n```python\nx = 1\n```",
		},
		{
			name: "bare basename",
			in:   "```Dockerfile\nFROM alpine\n```",
			want: "```dockerfile\nFROM alpine\n```",
		},
		{
			name: "already tagged is untouched",
			in:   "```rust\nfn main() {}\n```",
			want: "```rust\nfn main() {}\n```",
		},
		{
			name: "unknown extension left blank",
			in:   "```\nplain text\n```",
			want: "```\nplain text\n```",
		},
		{
			name: "text inside a block is not treated as a fence",
			in:   "```\nsee foo.go here\n```",
			want: "```\nsee foo.go here\n```",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tagCodeFences(c.in); got != c.want {
				t.Errorf("tagCodeFences(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}
