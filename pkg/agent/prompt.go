package agent

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dougkirkley/dross/pkg/provider"
)

// workspaceOrientation tells a session what it is sitting in.
//
// A session started on a repository used to open blind: the first moves were
// ls -R, a glob and a read of main.go — several turns spent working out where
// it was, on a workspace that was put there deliberately and whose name was
// known before the session existed. Asked what to do with "this repo", it could
// only ask back.
//
// The instruction not to clone is not hypothetical. Asked to build the project
// it was already standing in, a model answered with `git clone` and a
// placeholder URL, because nothing had told it the code was under its feet.
func workspaceOrientation(repo, branch string) string {
	if repo == "" {
		return ""
	}
	b := "\n\n## This workspace\n\nIt is a checkout of " + repo
	if branch != "" {
		b += ", on branch " + branch + ", made for this session"
	}
	return b + `. The code is already here: never clone it, and never ask for it to be uploaded.
- Look before asking. Which project this is, and what is in it, are answered by the files around you.
- Asked to build, install, test or run this project, do it here and report what happened. A list of ways it could be done is not an answer to "do it".
- Commit on this branch. Do not switch to the branch it was cut from.`
}

// projectFiles are the documents a repository uses to explain itself, most
// specific first: two of them are written for an agent, and the third is
// written for whoever turns up.
var projectFiles = []string{"AGENTS.md", "CLAUDE.md", "README.md"}

// docBudget bounds what is quoted from any one of them.
//
// A README can be a book. What earns its place in a system prompt is the top of
// it — what the project is, how it is built, how it is run — which is what the
// first few thousand characters of one almost always are.
const docBudget = 6000

// projectDocs puts the repository's own introduction in front of the model.
//
// Telling a session to read the README is not the same as it having read one:
// a model that opens by asking what the project is has already spent the turn
// that mattered. These are the files a person would be handed on their first
// day, they are small, and they are the difference between a session that knows
// what it is looking at and one that goes looking.
func projectDocs(workdir string) string {
	if workdir == "" {
		return ""
	}
	var b strings.Builder
	for _, name := range projectFiles {
		body, err := os.ReadFile(filepath.Join(workdir, name))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			continue
		}
		if len(text) > docBudget {
			// Cut on a line, so the quote does not end mid-sentence and read as
			// though the file itself stops there.
			text = text[:docBudget]
			if i := strings.LastIndexByte(text, '\n'); i > 0 {
				text = text[:i]
			}
			text += "\n\n[…truncated. Read " + name + " for the rest.]"
		}
		b.WriteString("\n\n## " + name + "\n\n" + text)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nThe workspace explains itself in the following. It is quoted as" +
		" it was when this session started; read the files themselves if you need" +
		" them exactly." + b.String()
}

// mapEntries and mapBudget bound the workspace listing.
//
// The point is orientation, not an inventory: a few hundred paths is enough for
// a model to know where things live and what the project is made of, and a
// monorepo's full file list would crowd out the rest of the prompt for no gain.
const (
	mapEntries = 200
	mapBudget  = 4000
)

// workspaceMap lists what is in the workspace, and where git thinks it stands.
//
// A session used to open blind. Every task in the benchmark spent its first
// turn running a glob or an ls to discover a five-file directory — a whole
// model round trip to learn something the process already knew before the
// session started. Putting it in the system prompt costs nothing per turn: it
// is computed once and sits inside the cached prefix.
//
// This is deliberately not a substitute for looking. It says what exists, never
// what is in it, and it is labelled as a snapshot so a model that has since
// created a file does not trust a stale list over its own eyes.
func workspaceMap(workdir string) string {
	if workdir == "" {
		return ""
	}
	var files []string
	truncated := false
	filepath.WalkDir(workdir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && p != workdir {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= mapEntries {
			truncated = true
			return filepath.SkipAll
		}
		if rel, err := filepath.Rel(workdir, p); err == nil {
			files = append(files, rel)
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)

	list := strings.Join(files, "\n")
	if len(list) > mapBudget {
		list = list[:mapBudget]
		if i := strings.LastIndexByte(list, '\n'); i > 0 {
			list = list[:i]
		}
		truncated = true
	}

	b := "\n\n## Files here at session start\n\n" +
		"A snapshot, so you do not have to spend a turn discovering the layout. " +
		"It says what exists, not what is in it, and it does not reflect anything " +
		"changed since — use glob and read for that.\n\n```\n" + list + "\n"
	if truncated {
		b += "[…truncated. Use glob for the rest.]\n"
	}
	b += "```"
	if st := gitState(workdir); st != "" {
		b += "\n\n" + st
	}
	return b
}

// gitState reports the branch and the working tree's dirtiness, when the
// workspace is a repository. What a session is standing on and whether it is
// clean are the two facts it would otherwise ask git for on turn one.
func gitState(workdir string) string {
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = workdir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	branch := run("rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		return ""
	}
	s := "Git: on branch " + branch
	status := run("status", "--porcelain")
	if status == "" {
		return s + ", working tree clean."
	}
	lines := strings.Split(status, "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], fmt.Sprintf("[…and %d more]", len(lines)-20))
	}
	return s + ", with uncommitted changes:\n\n```\n" + strings.Join(lines, "\n") + "\n```"
}

// codeToolLines describes the coding tools a session actually holds, one line
// each, in the order they matter.
//
// Generated from the session's real tool list rather than written out, because
// the written-out version went stale: it named four tools while the session was
// handed ten, so grep and glob were invisible and a model that wanted to search
// reached for `bash grep` instead — slower, noisier, and a turn it did not need
// to spend. A prompt that under-lists tools does not make a model careful, it
// makes it improvise.
func codeToolLines(tools []string) string {
	desc := map[string]string{
		"read":         "read(path) or read(paths=[…]): read one file, or several in a single call. Content comes back with line numbers. When you already know which files you want, ask for them all at once.",
		"write":        "write(path, content): create or overwrite a file.",
		"edit":         "edit(path, old_str, new_str): replace an exact substring in a file. old_str must match the file exactly, including whitespace. Pass replace_all for a rename that touches every occurrence, and edits=[…] to make several changes in a single call rather than one call each — give an entry its own path and one call changes several files, which is how a rename across a package should be done.",
		"grep":         "grep(pattern, path?): search file contents for a regular expression. Use this to search the workspace, not `bash grep`. Pass context=N to get the surrounding lines in the same call instead of a follow-up read, files_only for just the file names, and glob to restrict which files are searched.",
		"glob":         "glob(pattern): list files whose paths match a glob (**/*.go). Use this to find files, not `bash find` or `ls -R`.",
		"todo":         "todo(items): keep the plan for a multi-step job. Send the whole list each time; it comes back to you, so it is also how you remember what is left. Not for one-step tasks.",
		"bash":         "bash(command): run a shell command with the workspace as the working directory. Set background=true for anything long-lived.",
		"shell_output": "shell_output(id): read (or kill) a background shell started by bash.",
		"monitor":      "monitor(command): poll a command until it exits 0 — wait for a server or a build without burning turns.",
		"web_search":   "web_search(query): search the web.",
		"web_fetch":    "web_fetch(url): fetch a URL and return its readable text.",
	}
	// A fixed order, so the prompt reads the same whatever order the tools were
	// registered in — and so the cache prefix is stable across sessions.
	order := []string{"read", "write", "edit", "grep", "glob", "bash", "shell_output", "monitor", "todo", "web_search", "web_fetch"}
	have := map[string]bool{}
	for _, t := range tools {
		have[t] = true
	}
	var b strings.Builder
	for _, name := range order {
		// No list at all means the caller did not say; fall back to describing
		// the full built-in set rather than an empty prompt.
		if len(tools) > 0 && !have[name] {
			continue
		}
		b.WriteString("- " + desc[name] + "\n")
	}
	return b.String()
}

// basePrompt documents the tools and the working style for every agent. A
// Config.Identity is appended after it, so callers write only the role-specific
// framing while tool behavior stays consistent.
func basePrompt(tools []string, confined bool) string {
	reach := `Relative paths resolve against the workspace root. You may also read and
write elsewhere on this machine with an absolute path, so a sibling checkout or
a file in the home directory is reachable — say which path you are touching when
it is outside the workspace.`
	if confined {
		reach = `All paths are relative to the workspace root; paths that escape it are
rejected. This session is sandboxed: you cannot read or write outside it, so do
not offer to.`
	}
	return `You are a coding agent operating inside a workspace directory.

You have these tools:
` + codeToolLines(tools) + `
` + reach + `

Working style:
- When you have enough information to act, act. Do not narrate a plan you will not follow.
- Ask for everything you need at once. read takes a paths array, so six files is
  one call; and read, grep, glob and web_fetch calls in a single reply are all
  executed at the same time. Batch every look-up whose target you already know —
  waiting for one file before naming the next is the slowest way to work.
- Verify your work: after editing code, build or run the relevant tests via bash.
- Keep responses concise. Lead with the outcome.
- Report completion only when the task is actually done. If something is blocked, say so plainly.
- Plain markdown only, and no HTML: a <br> is printed as those four characters, not as a line break.
- Prefer prose, short lists and code blocks to tables. This is often read on a phone, where a three-column table is three words wide.`
}

// chatBasePrompt documents the working style for a chat session: no filesystem,
// shell, or codebase — just conversation, provided context, and the web.
// chatPrompt describes a conversational session, including the retrieval tools
// it actually has.
//
// It used to say "you have one tool: web_fetch" while the session was handed
// web_search as well - so the model was never told it could search, and
// answered from memory because as far as it knew that was the only option. A
// prompt that lists the wrong tools is worse than one that lists none: it
// actively rules out the right move.
//
// The directive to look things up is deliberately strong. A local model cannot
// tell how stale its training data is, and the failure mode is not a refusal
// but a confident wrong answer with no way for the reader to check it.
func chatPrompt(tools []string) string {
	has := func(n string) bool {
		for _, t := range tools {
			if t == n {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	b.WriteString(`You are a conversational assistant. You have no filesystem, no shell, and no access to the user's codebase — you cannot read, write, or edit files, run commands, or inspect a repository.

Your tools:
`)
	switch {
	case has("web_search_exa"):
		b.WriteString("- web_search_exa(query): search the web and get ranked results with snippets. This is your best search; prefer it.\n")
	case has("web_search"):
		b.WriteString("- web_search(query): search the web and get ranked results with snippets.\n")
	}
	if has("web_search_advanced_exa") {
		b.WriteString("- web_search_advanced_exa(query): a slower, more thorough search. Worth it when the plain one comes back thin, or the question turns on something specific.\n")
	}
	if has("web_search") && has("web_search_exa") {
		b.WriteString("- web_search(query): a second search backend, if the first returns nothing useful.\n")
	}
	if has("web_fetch_exa") {
		b.WriteString("- web_fetch_exa(url): fetch a page and return its readable text.\n")
	} else if has("web_fetch") {
		b.WriteString("- web_fetch(url): fetch a page and return its readable text.\n")
	}

	b.WriteString(`
Looking things up:
- Search before answering anything factual, current, comparative, or checkable —
  prices, products, versions, releases, people, events, "best" or "latest"
  anything. Search first, then answer from what came back.
- For anything people have opinions about — products, purchases, "best X",
  comparisons, "is X worth it", troubleshooting — run a second and third search
  with "reddit" and "youtube review" in the query itself. The first page of
  results for a buying question is written by people paid to rank; the useful
  part is usually someone who owns the thing and a video of it being used. Cite
  those alongside the rest.
- Put those words in the query rather than using a site: filter. Search backends
  here either ignore that operator or return nothing for it, and a search that
  returns nothing is worse than a plainer one that returns something.
- Your training data is stale and you cannot tell how stale. Treat memory as a
  starting point for what to search for, not as the answer.
- Fetch the promising results. A snippet is a reason to open a page, not a
  source in itself.
- If you did not look something up, say so plainly rather than implying you did.
- Link inline as you write, in markdown: "the [Wirecutter pick](https://…) is
  the Saatva". Put the link on the claim it supports rather than collecting them
  at the end - the reader is checking that sentence, not the paragraph.
- Never write a URL you have not fetched. Made-up links are worse than none:
  they look checkable, and the reader only finds out by following one. Links to
  pages that were not retrieved are removed before anyone sees them, so an
  invented one costs you the citation and leaves the sentence looking
  unsupported.

Working style:
- Answer from this conversation, any context or files the user has provided, and
  the web.
- Lead with the answer, then concise detail. Keep code and exact quotes verbatim.
- Never claim to have done something you can't — touching files, running code,
  seeing a repo. If a task genuinely needs that, say so and suggest a Code
  session.`)
	return b.String()
}

// delegateGuidance is appended to a lead agent's prompt when a worker model is
// available. It is the cost lever: push bounded, self-contained work to the
// cheaper model and keep expensive context small.
const delegateGuidance = `

You also have a task(task) tool backed by a cheaper worker model. Use it to
save cost and context:
- Hand over bounded, mechanical, self-contained subtasks — a single-function
  implementation, a rename across known sites, a file summary, a test stub, a
  search-and-extract.
- The task string must be FULLY self-contained: the worker cannot see this
  conversation, so spell out every file path, signature, and constraint it
  needs. Ambiguous delegation produces confidently wrong output.
- The worker shares your workspace and returns a conclusion, not raw file
  contents. Treat its output as a draft — verify load-bearing results (compile,
  run tests) before trusting them.
- Keep judgment, planning, and cross-file reasoning for yourself. Hand over the
  hands, not the head.`

// planGuidance frames a session that has been asked to plan before it acts.
//
// The gate already refuses the tools that change things, so this is not what
// makes plan mode safe — it is what makes it useful. Without it a model spends
// its turns discovering the refusal one tool at a time, which is a slow way to
// be told something it could have been told once.
const planGuidance = `

## Plan first

This session is planning. You can read, search and run nothing: every tool that
changes a file, runs a command, or spends money is unavailable until the plan is
agreed.

- Look first, and look properly. The plan is only worth what you know when you
  write it, and reading is the one thing you can still do.
- Then call exit_plan_mode with the plan: what you will do, in the order you
  will do it, concretely enough that someone could disagree with a step.
- It goes to the user. If they accept, the rest of the tools come back and you
  start on step one. If they do not, you are still planning — revise and ask
  again.
- Do not narrate the plan in prose and stop. A plan that was not passed to
  exit_plan_mode was never put to anyone.`

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

const sandboxGuidance = `

This is a sandboxed session. HOME, PATH, and every package-manager prefix
(go, npm, pip, cargo, ...) point inside the workspace, so anything you install
stays in this session and is immediately on PATH — you keep access to all global
tools and env, but must never write outside the workspace.
- Install with the normal commands; they already land in the sandbox: ` + "`go install`" + `,
  ` + "`npm install -g`" + `, ` + "`pip install --user`" + `, ` + "`cargo install`" + `.
- Go is already installed and on PATH (` + "`go version`" + `); use it directly.
- Any OTHER language toolchain/SDK (Node, a compiler, a different Go version) is
  NOT installed with the system package manager here (apt/apk/brew need root,
  which you do not have and will not get). Instead download the official tarball
  and unpack it under $DROSS_TOOLS — $DROSS_TOOLS/<name>/bin is already on PATH,
  so it works in your next command. For example (this host is linux/amd64):
    curl -fsSL https://nodejs.org/dist/v22.11.0/node-v22.11.0-linux-x64.tar.xz | tar -C "$DROSS_TOOLS" -xJ
  The same pattern (fetch tarball -> extract to $DROSS_TOOLS) installs any SDK.
- NEVER use sudo or doas, and never a system package manager (apt/apk/brew) or a
  path outside the workspace — you have no root, don't need it, and such changes
  won't persist. $DROSS_WORKSPACE is the workspace root; $DROSS_TOOLS holds SDKs.`

func systemPrompt(cfg Config, hasWorker, hasPublish bool, tools []string) string {
	if cfg.Chat {
		// Chat sessions have no coding tools; use the conversational base and
		// skip the delegate/publish/sandbox guidance entirely.
		p := chatPrompt(tools)
		if d := styleDirective(cfg.Style); d != "" {
			p += "\n" + d
		}
		if cfg.Identity != "" {
			p += "\n\n" + cfg.Identity
		}
		return p
	}
	p := basePrompt(tools, cfg.Sandbox)
	if cfg.Plan {
		p += planGuidance
	}
	if hasWorker {
		p += delegateGuidance
	}
	if hasPublish {
		p += publishGuidance
	}
	if cfg.Sandbox {
		p += sandboxGuidance
	}
	p += workspaceOrientation(cfg.Repo, cfg.Branch)
	p += projectDocs(cfg.Workdir)
	p += workspaceMap(cfg.Workdir)
	if d := styleDirective(cfg.Style); d != "" {
		p += "\n" + d
	}
	if cfg.Identity != "" {
		p += "\n\n" + cfg.Identity
	}
	return p
}

// toolDefs is the fixed day-one tool set. The task tool is added only when
// a worker model is configured. Correctness over breadth; more tools (glob,
// symbol search, screenshot-publish) come in later phases.
func toolDefs(hasWorker, hasPublish, hasAsker bool) []provider.ToolDef {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	defs := []provider.ToolDef{
		{
			Name: "read",
			Description: "Read files from the workspace. Returns line-numbered content. " +
				"Pass paths (an array) to read SEVERAL files in one call — always prefer that " +
				"over one call per file when you already know which files you want.",
			Properties: map[string]any{
				"path": strProp("File path relative to the workspace root."),
				"paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Several file paths to read at once, relative to the workspace root.",
				},
			},
		},
		{
			Name:        "write",
			Description: "write: create or overwrite a file in the workspace.",
			Properties: map[string]any{
				"path":    strProp("File path relative to the workspace root."),
				"content": strProp("Full file contents to write."),
			},
			Required: []string{"path", "content"},
		},
		{
			Name: "edit",
			Description: "edit: replace exact substrings in files. Pass old_str/new_str for one replacement, " +
				"or edits (an array) to make SEVERAL changes in one call — always prefer that over one call per " +
				"change. Entries may each carry their own path, so a rename across several files is ONE edit " +
				"call, not one per file. They are applied all together, so if any one fails nothing is written " +
				"anywhere. old_str must match exactly, including whitespace, and must be unique unless " +
				"replace_all is set.",
			Properties: map[string]any{
				"path":        strProp("File path relative to the workspace root. With edits, the default for entries that do not name their own."),
				"old_str":     strProp("Exact substring to replace, including whitespace. Must be unique in the file unless replace_all is set."),
				"new_str":     strProp("Replacement text."),
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match. Use for a rename. Default false."},
				"edits": map[string]any{
					"type": "array",
					"description": "Several replacements, applied in order and all together. Use this instead of " +
						"several edit calls. Give an entry its own path to change a different file in the same call.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_str":     strProp("Exact substring to replace, including whitespace."),
							"new_str":     strProp("Replacement text."),
							"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence of this old_str."},
							"path":        strProp("File this entry changes. Defaults to the file the entry before it named, or the call's path; set it when the batch moves to a different file."),
						},
						"required": []string{"old_str", "new_str"},
					},
				},
			},
			Required: []string{"path"},
		},
		{
			Name:        "bash",
			Description: "bash: run a shell command with the workspace as the working directory. Use it for building, running tests, and anything else a shell does — but prefer grep for searching file contents and glob for listing files, which return cleaner results. Set background=true for a long-running command (a dev server, watcher, tail); it returns a shell id immediately instead of blocking, and you read its output later with shell_output.",
			Properties: map[string]any{
				"command":    strProp("The shell command to run."),
				"background": map[string]any{"type": "boolean", "description": "Run detached and return a shell id instead of waiting. Default false."},
			},
			Required: []string{"command"},
		},
		{
			Name:        "shell_output",
			Description: "shell_output: read a background shell's status, runtime, and captured output by id (from a background bash call). Set kill=true to stop it.",
			Properties: map[string]any{
				"id":   strProp("The background shell id (e.g. sh_1)."),
				"kill": map[string]any{"type": "boolean", "description": "Stop the shell before returning. Default false."},
			},
			Required: []string{"id"},
		},
		{
			Name:        "monitor",
			Description: "monitor: wait for a condition by polling a shell command until it exits 0 (or until timeout). Use to wait on async state without burning turns — a server coming up, a file appearing, a build or CI run finishing. Returns as soon as the command first succeeds.",
			Properties: map[string]any{
				"command":          strProp("Shell command to poll; success is exit code 0 (e.g. `curl -sf localhost:8080/health`)."),
				"interval_seconds": map[string]any{"type": "number", "description": "Seconds between checks. Default 5."},
				"timeout_seconds":  map[string]any{"type": "number", "description": "Give up after this many seconds. Default 300."},
			},
			Required: []string{"command"},
		},
		{
			Name:        "web_search",
			Description: "web_search: search the web and get a ranked list of results (title, URL, snippet). This is the internet search; to search files in the workspace use grep instead. Use it to find current information or the right page, then web_fetch a result to read it.",
			Properties:  map[string]any{"query": strProp("The search query.")},
			Required:    []string{"query"},
		},
		{
			Name:        "web_fetch",
			Description: "web_fetch: fetch an http(s) URL and return its text content (HTML is reduced to readable text). Use for documentation, references, or pages the user links, or a result from web_search.",
			Properties:  map[string]any{"url": strProp("The absolute http(s) URL to fetch.")},
			Required:    []string{"url"},
		},
		{
			Name: "grep",
			Description: "grep: search the contents of files in the workspace for a regular expression. This is the workspace search; " +
				"to search the web use web_search instead. Returns matching lines as path:line:text. Pass context to get the " +
				"surrounding lines back in the same call, which usually saves a follow-up read.",
			Properties: map[string]any{
				"pattern": strProp("Regular expression to search for."),
				"path":    strProp("Optional file or directory to search (relative to the workspace root; default whole workspace)."),
				"context": map[string]any{"type": "integer", "description": "Lines of context to show either side of each match (max 20). Default 0."},
				"glob":    strProp("Optional glob restricting which files are searched (e.g. **/*.go)."),
				"files_only": map[string]any{"type": "boolean",
					"description": "Return only the names of files that match, not the matching lines. Use when you want to know where something lives."},
			},
			Required: []string{"pattern"},
		},
		{
			Name:        "glob",
			Description: "glob: list the files in the workspace whose paths match a glob pattern (e.g. **/*.go, cmd/*/main.go). This is how to find files by name; to search their contents use grep.",
			Properties:  map[string]any{"pattern": strProp("Glob pattern relative to the workspace root.")},
			Required:    []string{"pattern"},
		},
		{
			Name: "todo",
			Description: "todo: write down the plan for a job with more than a couple of steps, and keep it current. " +
				"Send the WHOLE list every time — it replaces the previous one. Mark a step in_progress when you " +
				"start it and completed the moment it is done, in the same call that adds the next one where you " +
				"can. Skip it for a one-step task: a list of one is overhead. It is returned to you after every " +
				"call, so it is also how you remember what is left.",
			Properties: map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "The complete list, in order. Replaces the previous list.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"task":   strProp("What is to be done, in a few words."),
							"status": strProp("pending, in_progress, or completed."),
						},
						"required": []string{"task"},
					},
				},
			},
			Required: []string{"items"},
		},
	}
	if hasWorker {
		defs = append(defs, provider.ToolDef{
			Name:        "task",
			Description: "task: hand a self-contained subtask to a cheaper worker model. Pass `task` for one subtask, or `tasks` (an array) to run several INDEPENDENT subtasks in parallel (bounded by the configured max). Each task must include all context the worker needs; it cannot see this conversation. Returns the worker conclusion(s). Only parallelize tasks that don't touch the same files.",
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
			Description: "Publish a workspace file and get a link back. Use for screenshots, images, gifs, or reports the user wants to view. The link is private — only the signed-in user can open it. Do not describe it as public or shareable; the user can create a public share link themselves if they want one.",
			Properties:  map[string]any{"path": strProp("Workspace-relative path to the file to publish.")},
			Required:    []string{"path"},
		})
	}
	if hasAsker {
		defs = append(defs, provider.ToolDef{
			Name:        "ask_user",
			Description: "ask_user: ask the user 1-4 clarifying questions at once when blocked on decisions only they can make (ambiguous requirements, real choices with no sensible default). Each question gets its own tab; the user picks an option per question, then submits. Returns their answers. Use sparingly — prefer sensible defaults.",
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
