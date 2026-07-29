package agent

import "github.com/dougkirkley/ergon/pkg/provider"

// basePrompt documents the tools and the working style for every agent. A
// Config.Identity is appended after it, so callers write only the role-specific
// framing while tool behavior stays consistent.
const basePrompt = `You are a coding agent operating inside a confined workspace directory.

You have these tools:
- read(path): read a file; content is returned with line numbers.
- write(path, content): create or overwrite a file.
- edit(path, old_str, new_str): replace an exact, unique substring in a file. old_str must match the file exactly, including whitespace, and match exactly once — include surrounding context to make it unique.
- bash(command): run a shell command with the workspace as the working directory.

All paths are relative to the workspace root; paths that escape it are rejected.

Working style:
- When you have enough information to act, act. Do not narrate a plan you will not follow.
- Verify your work: after editing code, build or run the relevant tests via bash.
- Keep responses concise. Lead with the outcome.
- Report completion only when the task is actually done. If something is blocked, say so plainly.`

// delegateGuidance is appended to a lead agent's prompt when a worker model is
// available. It is the cost lever: push bounded, self-contained work to the
// cheaper model and keep expensive context small.
const delegateGuidance = `

You also have a delegate(task) tool backed by a cheaper worker model. Use it to
save cost and context:
- Delegate bounded, mechanical, self-contained subtasks — a single-function
  implementation, a rename across known sites, a file summary, a test stub, a
  search-and-extract.
- The task string must be FULLY self-contained: the worker cannot see this
  conversation, so spell out every file path, signature, and constraint it
  needs. Ambiguous delegation produces confidently wrong output.
- The worker shares your workspace and returns a conclusion, not raw file
  contents. Treat its output as a draft — verify load-bearing results (compile,
  run tests) before trusting them.
- Keep judgment, planning, and cross-file reasoning for yourself. Delegate the
  hands, not the head.`

// briefPrompt asks for a compact handoff at the end of a run. It is the core
// context-economy lever: the next session resumes from this, not the transcript.
const briefPrompt = `Write a handoff brief for a completely fresh session that will continue this work with no other context. This brief is the ONLY thing the next session will see — the full transcript is discarded. Be compact but complete:
- Task: what is being done and why.
- State: what works so far.
- Files: the specific files created/changed and their purpose.
- Next: the concrete next steps.
- Gotchas: decisions, constraints, and anything that would trip up a fresh start.
Terse markdown. Omit anything the next session can cheaply rediscover.`

// workerIdentity frames a delegated sub-agent: one bounded task, concise result.
const workerIdentity = `You are a worker sub-agent. You are given one bounded, self-contained task.
Do exactly that task using your tools, then stop. Return a short, factual
summary of what you did and the concrete result (file paths changed, the value
produced, or the command output that matters) — not a narration of your steps.
Do not expand the scope beyond the task as given.`

// styleDirective returns the output-verbosity instruction for a style. Terser
// styles cut output tokens; all keep code, commands, and errors verbatim.
func styleDirective(s Style) string {
	switch s {
	case StyleTerse:
		return `
Output style: terse. No preamble, no restating the task, no summaries of routine actions. Fragments over sentences. One line per point. Keep code, commands, and exact errors verbatim.`
	case StyleCaveman:
		return `
Output style: caveman. Maximally compressed. Drop articles (a/an/the) and filler (just/really/basically). Fragments only. Short words. No pleasantries, no narration of what you are about to do. Keep ALL technical substance — code blocks, file paths, commands, and exact error strings — verbatim and unabbreviated.`
	default:
		return ""
	}
}

const publishGuidance = `

You also have a publish(path) tool: it shares a workspace file at a public URL
and returns the link. Use it when the user wants to see or share a produced
artifact — a screenshot, a rendered image or gif, an exported report. Include
the returned URL in your reply.`

func systemPrompt(cfg Config, hasWorker, hasPublish bool) string {
	p := basePrompt
	if hasWorker {
		p += delegateGuidance
	}
	if hasPublish {
		p += publishGuidance
	}
	if d := styleDirective(cfg.Style); d != "" {
		p += "\n" + d
	}
	if cfg.Identity != "" {
		p += "\n\n" + cfg.Identity
	}
	return p
}

// toolDefs is the fixed day-one tool set. The delegate tool is added only when
// a worker model is configured. Correctness over breadth; more tools (glob,
// symbol search, screenshot-publish) come in later phases.
func toolDefs(hasWorker, hasPublish, hasAsker bool) []provider.ToolDef {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	defs := []provider.ToolDef{
		{
			Name:        "read",
			Description: "Read a file from the workspace. Returns line-numbered content.",
			Properties:  map[string]any{"path": strProp("File path relative to the workspace root.")},
			Required:    []string{"path"},
		},
		{
			Name:        "write",
			Description: "Create or overwrite a file in the workspace.",
			Properties: map[string]any{
				"path":    strProp("File path relative to the workspace root."),
				"content": strProp("Full file contents to write."),
			},
			Required: []string{"path", "content"},
		},
		{
			Name:        "edit",
			Description: "Replace an exact, unique substring in a file. old_str must match exactly once.",
			Properties: map[string]any{
				"path":    strProp("File path relative to the workspace root."),
				"old_str": strProp("Exact substring to replace, including whitespace. Must be unique in the file."),
				"new_str": strProp("Replacement text."),
			},
			Required: []string{"path", "old_str", "new_str"},
		},
		{
			Name:        "bash",
			Description: "Run a shell command with the workspace as the working directory. Use for building, running tests, searching, and listing files. Set background=true for a long-running command (a dev server, watcher, tail); it returns a shell id immediately instead of blocking, and you read its output later with shell_output.",
			Properties: map[string]any{
				"command":    strProp("The shell command to run."),
				"background": map[string]any{"type": "boolean", "description": "Run detached and return a shell id instead of waiting. Default false."},
			},
			Required: []string{"command"},
		},
		{
			Name:        "shell_output",
			Description: "Read a background shell's status, runtime, and captured output by id (from a background bash call). Set kill=true to stop it.",
			Properties: map[string]any{
				"id":   strProp("The background shell id (e.g. sh_1)."),
				"kill": map[string]any{"type": "boolean", "description": "Stop the shell before returning. Default false."},
			},
			Required: []string{"id"},
		},
		{
			Name:        "monitor",
			Description: "Wait for a condition by polling a shell command until it exits 0 (or until timeout). Use to wait on async state without burning turns — a server coming up, a file appearing, a build or CI run finishing. Returns as soon as the command first succeeds.",
			Properties: map[string]any{
				"command":          strProp("Shell command to poll; success is exit code 0 (e.g. `curl -sf localhost:8080/health`)."),
				"interval_seconds": map[string]any{"type": "number", "description": "Seconds between checks. Default 5."},
				"timeout_seconds":  map[string]any{"type": "number", "description": "Give up after this many seconds. Default 300."},
			},
			Required: []string{"command"},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch an http(s) URL and return its text content (HTML is reduced to readable text). Use for documentation, references, or pages the user links.",
			Properties:  map[string]any{"url": strProp("The absolute http(s) URL to fetch.")},
			Required:    []string{"url"},
		},
	}
	if hasWorker {
		defs = append(defs, provider.ToolDef{
			Name:        "delegate",
			Description: "Delegate to a cheaper worker model. Pass `task` for one subtask, or `tasks` (an array) to run several INDEPENDENT subtasks in parallel (bounded by the configured max). Each task must include all context the worker needs; it cannot see this conversation. Returns the worker conclusion(s). Only parallelize tasks that don't touch the same files.",
			Properties: map[string]any{
				"task": strProp("A fully self-contained subtask (single delegation)."),
				"tasks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Several independent self-contained subtasks to run in parallel.",
				},
			},
		})
	}
	if hasPublish {
		defs = append(defs, provider.ToolDef{
			Name:        "publish",
			Description: "Share a workspace file at a public URL and get the link back. Use for screenshots, images, gifs, or reports the user wants to see or share.",
			Properties:  map[string]any{"path": strProp("Workspace-relative path to the file to publish.")},
			Required:    []string{"path"},
		})
	}
	if hasAsker {
		defs = append(defs, provider.ToolDef{
			Name:        "ask_user",
			Description: "Ask the user 1-4 clarifying questions at once when blocked on decisions only they can make (ambiguous requirements, real choices with no sensible default). Each question gets its own tab; the user picks an option per question, then submits. Returns their answers. Use sparingly — prefer sensible defaults.",
			Properties: map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "The questions to ask (1-4).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"header":   strProp("Short tab label (<=12 chars)."),
							"question": strProp("The full question."),
							"options": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Distinct choices; put the recommended one first.",
							},
							"multiSelect": map[string]any{
								"type":        "boolean",
								"description": "Allow selecting multiple options for this question.",
							},
						},
						"required": []string{"question", "options"},
					},
				},
			},
			Required: []string{"questions"},
		})
	}
	return defs
}
