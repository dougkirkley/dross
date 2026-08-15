import 'dart:convert';

import 'package:flutter/material.dart';

import '../models.dart';
import '../theme.dart';
import 'modal.dart';
import 'readable.dart';
import '../util/nav.dart';

/// One step within an activity block: a tool call or a file edit.
class ActStep {
  ActStep(
      {required this.icon,
      required this.label,
      this.color = T.text,
      this.edit,
      this.detail,
      this.output,
      this.aside = false});
  final IconData icon;
  final String label;
  final Color color;
  final EditInfo? edit;

  /// Something the run said on its way to the next call, rather than something
  /// it did. Kept in the block so the sentence is not lost, and left out of the
  /// summary: "read 2 files, used 3 tools" where three of them are sentences
  /// describes work that never happened.
  final bool aside;

  /// The tool's full input (command, path, task) shown in the drill-in.
  final String? detail;

  /// The tool's result content, filled in when the tool-result event arrives.
  String? output;

  /// Whether the command behind this step ended non-zero.
  ///
  /// The bash tool returns a failure as ordinary content — the output, then an
  /// "[exit status: ...]" line — because a non-zero exit is information for the
  /// model rather than a broken tool. That makes a failed command look exactly
  /// like a successful one here, so the one thing worth reading sat behind a
  /// chevron while the model's account of it was the only thing on screen.
  bool get failed => (output ?? '').contains('[exit status:');

  /// Whether tapping the row opens a detail view (edits use their own diff view).
  bool get hasDetail =>
      edit == null &&
      ((output != null && output!.trim().isNotEmpty) ||
          (detail != null && detail!.isNotEmpty));
}

/// A run of consecutive tool/edit events between assistant messages, shown
/// collapsed as one line ("Used a tool, edited a file +A -B") that expands.
class Activity {
  final List<ActStep> steps = [];

  /// Whether any command in this run ended badly. A failure is the reason
  /// somebody is reading a transcript at all.
  bool get failed => steps.any((s) => s.failed);

  int get added => steps.fold(0, (s, e) => s + (e.edit?.added ?? 0));
  int get removed => steps.fold(0, (s, e) => s + (e.edit?.removed ?? 0));
  int get edits => steps.where((s) => s.edit != null).length;
  int get tools => steps.where((s) => s.edit == null && !s.aside).length;

  /// Enumerates the distinct actions taken, like Claude's "Edited a file, ran a
  /// command, read a file".
  String get summary {
    final counts = <String, int>{};
    for (final s in steps) {
      if (s.aside) continue; // said, not done
      final k = s.edit != null ? 'edit' : _verbKey(s.label);
      counts[k] = (counts[k] ?? 0) + 1;
    }
    const order = [
      'edit',
      'bash',
      'read',
      'search',
      'delegate',
      'publish',
      'tool'
    ];
    final phrases = <String>[];
    for (final k in order) {
      final n = counts[k];
      if (n != null) phrases.add(_phrase(k, n));
    }
    if (phrases.isEmpty) return 'Working…';
    final s = phrases.join(', ');
    return s[0].toUpperCase() + s.substring(1);
  }

  static String _verbKey(String label) {
    if (label.startsWith('Read')) return 'read';
    if (label.startsWith('Ran')) return 'bash';
    if (label.startsWith('Searched')) return 'search';
    if (label.startsWith('Delegated')) return 'delegate';
    if (label.startsWith('Published')) return 'publish';
    return 'tool';
  }

  static String _phrase(String k, int n) {
    switch (k) {
      case 'edit':
        return n == 1 ? 'edited a file' : 'edited $n files';
      case 'bash':
        return n == 1 ? 'ran a shell command' : 'ran $n shell commands';
      case 'read':
        return n == 1 ? 'read a file' : 'read $n files';
      case 'search':
        return 'searched the web';
      case 'delegate':
        return n == 1 ? 'delegated a task' : 'delegated $n tasks';
      case 'publish':
        return n == 1 ? 'published an artifact' : 'published $n artifacts';
      default:
        return n == 1 ? 'used a tool' : 'used $n tools';
    }
  }
}

/// Turns a flat event list into a timeline of Events (prompt/text/done/error/
/// checkpoint) interleaved with Activity blocks (tool/edit runs).
/// Drops the setup line a starting panel is currently showing.
///
/// The two say the same sentence, and while the panel is up it is the one
/// saying it: bigger, at the top, and about the state the session is in right
/// now. [setup] is empty once the session is ready, so the line comes back and
/// the transcript keeps the whole record.
List<Event> withoutLiveSetup(List<Event> events, String setup) {
  if (setup.isEmpty) return events;
  return events
      .where((e) => !(e.baseKind == 'meta' && e.data == setup))
      .toList();
}

/// How long a line may be and still be a preamble rather than an answer.
const _preambleMax = 160;

/// The shapes an announcement takes.
///
/// Length alone was the wrong test and folded the wrong things. "Now a test
/// pinning the cap." is twenty-seven characters and is the most useful line in
/// its turn; "Let me check what files we have in the workspace" is forty-eight
/// and says nothing the tool row underneath it does not. The difference is not
/// size, it is grammar: one reports, the other announces.
///
/// An announcement either opens with an intention — let me, let's, I'll, I'm
/// going to, first/next/now let's — or ends in a colon, which is a sentence
/// pointing at what comes after it. A conclusion does neither.
final _announces = RegExp(
  r"^(ok(ay)?[,.]?\s+)?(now\s+)?(let me\b|let's\b|lets\b|i'll\b|i will\b|i'm going to\b|i am going to\b|first[,.]?\s+let|next[,.]?\s+let)",
  caseSensitive: false,
);

/// Whether the text at [i] is the run announcing its next tool call.
///
/// Two conditions, both needed. Short, and immediately followed by a tool or an
/// edit — with the bookkeeping events that carry nothing skipped, because a
/// usage counter between the sentence and the call does not make the sentence
/// an answer.
bool _isPreamble(List<Event> events, int i) {
  final text = events[i].data.trim();
  if (text.isEmpty || text.length > _preambleMax) return false;
  // Short is necessary and nowhere near sufficient: it has to read as an
  // announcement, or a one-line finding gets folded away as though it were one.
  if (!_announces.hasMatch(text) && !text.endsWith(':')) return false;
  // Bounded, because buildTimeline runs on every event of a live session and
  // this runs per text event within it. Unbounded, a transcript whose tail is
  // all bookkeeping made each scan walk to the end, on every frame. The call a
  // preamble announces is one or two events away; four is slack, not a budget.
  final end = (i + 5) < events.length ? i + 5 : events.length;
  for (var j = i + 1; j < end; j++) {
    switch (events[j].baseKind) {
      case 'usage':
      case 'tool-result':
      case 'ask':
      case 'answer':
        continue; // bookkeeping; keep looking
      case 'tool':
      case 'edit':
        return true;
      default:
        return false;
    }
  }
  return false; // nothing followed: it was the last thing said, so it is the answer
}

List<Object> buildTimeline(List<Event> events, {bool running = false}) {
  final out = <Object>[];
  Activity? cur;
  void flush() {
    if (cur != null) {
      out.add(cur!);
      cur = null;
    }
  }

  // Map background-shell ids to their command, so a shell:exit can name it.
  final shellCmd = {for (final s in buildShells(events)) s.id: s.command};

  // The tool step still awaiting its result, so the following tool-result event
  // can be attached to it for the drill-in view.
  ActStep? awaiting;

  for (var i = 0; i < events.length; i++) {
    final e = events[i];
    // Background shells live in the tasks sheet; only surface a line when one
    // finishes, so the conversation shows "Ran a shell command".
    if (e.isShell) {
      if (e.kind == 'shell:exit') {
        final id = e.data.split('\x1F').first;
        final cmd = shellCmd[id] ?? '';
        (cur ??= Activity()).steps.add(ActStep(
            icon: Icons.terminal,
            label: cmd.isEmpty ? 'Ran a shell command' : 'Ran  ${_trim(cmd)}',
            detail: cmd.isEmpty ? null : cmd));
      }
      continue;
    }
    switch (e.baseKind) {
      // A line written only to announce the call that follows it. Folded into
      // the run rather than breaking it: a model that says "Let me check the
      // room file:" before each of six reads used to turn one collapsible
      // "Read six files" into six cards and six rows. The sentence is kept —
      // it expands with the step — it just stops being a headline.
      case 'text' when _isPreamble(events, i):
        // Not run through _trim. That caps a label at 56 characters for the
        // one-line tool rows, and applying it here cut sentences mid-word —
        // "what files we have in the workspace to und…" — while the row was
        // free to wrap anyway. A preamble is short by construction
        // (_preambleMax), so it can be given whole and left to wrap.
        (cur ??= Activity()).steps.add(ActStep(
            icon: Icons.chat_bubble_outline,
            label: e.data.trim().replaceAll(RegExp(r'\s+'), ' '),
            detail: e.data,
            color: T.textFaint,
            aside: true));
      case 'prompt':
      case 'text':
      case 'review':
      case 'error':
      case 'checkpoint':
      // A question the run is blocked on, so it breaks the activity run and
      // stands on its own rather than folding into a summary line. Distinct
      // from "ask", which is the agent's own note that it asked something.
      case 'ask-request':
      // The session narrating itself — cloning a repository, a model loading.
      // Not a tool call, and folding it into one put it under a collapsed
      // "Used a tool", which is exactly where nobody looks for the answer to
      // "is this doing anything?".
      case 'meta':
        flush();
        out.add(e);
      case 'done':
        flush(); // end the activity run; no visible "done" line
        break;
      case 'tool':
        final step = _toolStep(e.data);
        if (step != null) (cur ??= Activity()).steps.add(step);
        awaiting = step; // null for write/edit, so their result isn't attached
      case 'tool-result':
        if (awaiting != null) {
          awaiting.output = e.data;
          awaiting = null;
        }
      case 'usage': // token counter, not an activity step
      case 'ask': // the agent's note; the actionable one is ask-request
      case 'answer':
        break; // not shown as a step
      case 'compact':
        (cur ??= Activity()).steps.add(ActStep(
            icon: Icons.compress,
            label: 'Compacted ${e.data}',
            color: T.textFaint));
      case 'edit':
        final info = e.editInfo;
        if (info != null) {
          (cur ??= Activity()).steps.add(ActStep(
              icon: info.isNew ? Icons.note_add_outlined : Icons.edit_outlined,
              label: '${info.isNew ? 'Created' : 'Edited'} ${info.file}',
              edit: info));
        }
      default:
        (cur ??= Activity())
            .steps
            .add(ActStep(icon: Icons.bolt, label: e.data, color: T.accentHi));
    }
  }
  // The step still waiting on its result is the one happening now, and the row
  // under the transcript is already saying so. Listing it as well showed the
  // same command twice — "Ran go test ./..." above "Running go test ./..." —
  // one of them in the past tense before it had happened. It joins the list
  // when its result arrives, which is when it is genuinely something that was
  // done.
  if (running && awaiting != null && cur != null) {
    cur!.steps.remove(awaiting);
    if (cur!.steps.isEmpty) cur = null;
  }
  flush();
  return out;
}

/// A present-tense label for what the agent is currently doing, from the most
/// recent tool/edit event — e.g. "Editing chat_screen.dart", "Running a
/// command". Returns "Working…" when the latest step isn't a tool action.
String currentActivityLabel(List<Event> events) {
  for (final e in events.reversed) {
    switch (e.baseKind) {
      case 'edit':
        try {
          final info =
              EditInfo.fromJson(jsonDecode(e.data) as Map<String, dynamic>);
          return 'Editing ${info.file}';
        } catch (_) {
          return 'Editing a file';
        }
      case 'searched':
        // Searching is done and the model is the wait now. Without this the
        // last search stays on screen for the whole answer, which makes a
        // working model look like a stuck search.
        return 'Reading results…';
      case 'search':
        // The retrieval that happens before the model reads anything. Naming
        // the query turns a wait into a report: the same seconds pass, with
        // far less doubt about whether something is happening.
        return 'Searching ${_searchLabel(e.data)}';
      case 'tool':
        final s = _toolStep(e.data);
        if (s != null) return _present(s.label);
      case 'text':
      case 'prompt':
        return 'Working…';
    }
  }
  return 'Working…';
}

/// What to call a search in the activity line.
///
/// The queries dross runs are the question plus a site - "best mattresses
/// reddit" - so the site is the useful half to lead with when there is one, and
/// the query itself otherwise.
String _searchLabel(String query) {
  final q = query.trim();
  for (final site in const ['reddit', 'amazon', 'wirecutter', 'youtube']) {
    if (q.toLowerCase().endsWith(' $site')) {
      return '$site for "${q.substring(0, q.length - site.length - 1)}"';
    }
  }
  return 'the web for "$q"';
}

// Turn a past-tense step label into a present-tense running label.
String _present(String label) {
  if (label.startsWith('Read ')) return 'Reading ${label.substring(5)}';
  if (label == 'Ran a command') return 'Running a command';
  if (label.startsWith('Ran  ')) return 'Running ${label.substring(5)}';
  if (label == 'Delegated to worker') return 'Delegating to worker';
  if (label.startsWith('Delegated  ')) {
    return 'Delegating ${label.substring(11)}';
  }
  if (label == 'Published an artifact') return 'Publishing an artifact';
  if (label == 'Searched the web') return 'Searching the web';
  return label;
}

ActStep? _toolStep(String data) {
  final sp = data.indexOf(' ');
  final name = sp == -1 ? data : data.substring(0, sp);
  final arg = sp == -1 ? '' : data.substring(sp + 1);
  switch (name) {
    case 'write':
    case 'edit':
      return null; // represented by the richer "edit" event
    case 'read':
      final path = _argPath(arg);
      return ActStep(
          icon: Icons.description_outlined, label: 'Read $path', detail: path);
    case 'bash':
      final cmd = _argField(arg, 'command');
      return ActStep(
          icon: Icons.terminal,
          label: cmd.isEmpty ? 'Ran a command' : 'Ran  ${_trim(cmd)}',
          detail: cmd.isEmpty ? null : cmd);
    case 'delegate':
      final task = _argField(arg, 'task');
      return ActStep(
          icon: Icons.call_split,
          label: task.isEmpty
              ? 'Delegated to worker'
              : 'Delegated  ${_trim(task)}',
          detail: task.isEmpty ? null : task);
    case 'publish':
      return ActStep(icon: Icons.link, label: 'Published an artifact');
    case 'web_search':
    case 'web-search':
      return ActStep(
          icon: Icons.search,
          label: 'Searched the web',
          detail: _argField(arg, 'query'));
    default:
      // MCP tools are named "server__tool"; surface them as MCP calls.
      final sep = name.indexOf('__');
      if (sep > 0 && sep + 2 < name.length) {
        final server = name.substring(0, sep);
        final tool = name.substring(sep + 2);
        return ActStep(
            icon: Icons.hub_outlined,
            label: 'Called $server (MCP): $tool',
            detail: arg);
      }
      return ActStep(
          icon: Icons.build_outlined, label: 'Used $name', detail: arg);
  }
}

String _argPath(String arg) {
  try {
    final j = jsonDecode(arg) as Map<String, dynamic>;
    return j['path'] as String? ?? 'a file';
  } catch (_) {
    return 'a file';
  }
}

/// Pulls a string field out of a tool's JSON args (e.g. "command", "task").
String _argField(String arg, String key) {
  try {
    final j = jsonDecode(arg) as Map<String, dynamic>;
    final v = j[key];
    return v is String ? v.trim() : '';
  } catch (_) {
    return '';
  }
}

/// Collapses whitespace and clips to a single readable line.
String _trim(String s, [int max = 56]) {
  final one = s.replaceAll(RegExp(r'\s+'), ' ').trim();
  return one.length <= max ? one : '${one.substring(0, max - 1)}…';
}

/// The collapsed activity line under a message.
///
/// Tapping expands it in place rather than opening a view of its own. What ran
/// is part of reading the conversation — it belongs in the flow, next to the
/// reply it produced, not behind a route that loses your place to show it.
class ActivityBlock extends StatefulWidget {
  const ActivityBlock(this.activity, {super.key});
  final Activity activity;

  @override
  State<ActivityBlock> createState() => _ActivityBlockState();
}

class _ActivityBlockState extends State<ActivityBlock> {
  @override
  Widget build(BuildContext context) {
    final a = widget.activity;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          onTap: () => showActivitySheet(context, a),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 10),
            child: Row(
              children: [
                Flexible(
                  // Two lines, because the counts and the chevron eat the end
                  // of the first and "edited a file, ran 3 shell commands,
                  // used 2 to…" stops on the word that says what it used.
                  child: Text(a.summary,
                      style: metaFor(context),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis),
                ),
                if (a.added > 0 || a.removed > 0) ...[
                  const SizedBox(width: 8),
                  Text('+${a.added}',
                      style: const TextStyle(color: T.ok, fontSize: 13)),
                  const SizedBox(width: 4),
                  Text('-${a.removed}',
                      style: const TextStyle(color: T.danger, fontSize: 13)),
                ],
                const SizedBox(width: 4),
                // Points down when open: the content is below, not elsewhere.
                const Icon(Icons.chevron_right, size: 18, color: T.textFaint),
              ],
            ),
          ),
        ),
        // Asides stay behind the chevron. Showing them dim under the summary
        // was a correction to folding too much, and the folding is what got
        // fixed instead: a line only counts as an aside now if it reads as an
        // announcement, so what lands here is "let me check the other files"
        // and nothing that answers anything. A findings line stays prose and
        // renders as prose.
        //
        // The shape that leaves is the one worth having, and it is the one
        // Claude Code arrives at: substantive text at full size, one grey line
        // per run of tool calls, nothing else competing.
      ],
    );
  }
}

/// One step, expanded in place: the command, then what it printed.
///
/// A step with a diff still opens the diff view. A diff is a side-by-side
/// reading task that wants the room; shell output is a few lines you glance at
/// and move on from, which is the distinction that decides where it goes.
class _InlineStep extends StatefulWidget {
  const _InlineStep(this.step);
  final ActStep step;

  @override
  State<_InlineStep> createState() => _InlineStepState();
}

class _InlineStepState extends State<_InlineStep> {
  /// Null until somebody taps: a failed step opens itself, a successful one
  /// stays shut, and either can be toggled after that.
  bool? _tapped;

  bool get _open => _tapped ?? widget.step.failed;

  @override
  Widget build(BuildContext context) {
    final s = widget.step;
    final edit = s.edit;
    final out = s.output?.trimRight() ?? '';
    final wide = isWide(context);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          onTap: edit != null
              // A desktop window already has the room: pushing a whole view
              // over the conversation to read four lines of go.mod loses the
              // place you were reading. A phone does not, so it still opens
              // the page.
              ? (wide
                  ? () => setState(() => _tapped = !_open)
                  : () => Navigator.push(context,
                      MaterialPageRoute(builder: (_) => EditDiffView(edit))))
              : (s.hasDetail ? () => setState(() => _tapped = !_open) : null),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(s.failed ? Icons.error_outline : s.icon,
                    size: 16, color: s.failed ? T.danger : s.color),
                const SizedBox(width: 8),
                // The counts sit against the name they belong to rather than
                // against the right edge. On a wide window that edge is a long
                // way from "Created main.go", and a "+34 -0" alone out there
                // reads as a column of its own instead of as part of the line.
                Flexible(
                  child: Text(s.label,
                      style: bodyFor(context),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis),
                ),
                if (edit != null) ...[
                  const SizedBox(width: 10),
                  Text('+${edit.added}',
                      style: const TextStyle(color: T.ok, fontSize: 13)),
                  const SizedBox(width: 4),
                  Text('-${edit.removed}',
                      style: const TextStyle(color: T.danger, fontSize: 13)),
                ],
                const Spacer(),
                if (edit != null)
                  Icon(
                      wide
                          ? (_open ? Icons.expand_less : Icons.expand_more)
                          : Icons.chevron_right,
                      size: 16,
                      color: T.textFaint)
                else if (s.hasDetail)
                  Icon(_open ? Icons.expand_less : Icons.expand_more,
                      size: 16, color: T.textFaint),
              ],
            ),
          ),
        ),
        // On a desktop width the output is plain indented text under the "└",
        // the way it reads in the terminal. On a phone it keeps the card: a
        // bordered block is what separates it from the conversation when the
        // indent is only a few pixels of a narrow screen.
        if (_open && edit != null && wide)
          Container(
            width: double.infinity,
            margin: const EdgeInsets.only(left: 28, bottom: 6),
            child: EditDiffLines(edit, inline: true),
          ),
        if (_open && edit == null)
          Container(
            width: double.infinity,
            margin: EdgeInsets.only(left: wide ? 28 : 16, bottom: 6),
            padding: wide ? EdgeInsets.zero : const EdgeInsets.all(10),
            decoration: wide
                ? null
                : BoxDecoration(
                    color: T.surface,
                    borderRadius: BorderRadius.circular(T.rCard),
                    border: Border.all(color: T.borderFaint),
                  ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (s.detail != null && s.detail!.isNotEmpty)
                  SelectableText('\$ ${s.detail}',
                      style: T.mono.copyWith(color: T.textFaint)),
                if (s.detail != null && s.detail!.isNotEmpty && out.isNotEmpty)
                  const SizedBox(height: 8),
                if (out.isNotEmpty)
                  // Wide output scrolls in its own box rather than forcing the
                  // conversation to wrap around it.
                  SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: SelectableText(out,
                        style: T.mono.copyWith(color: T.text, height: 1.4)),
                  )
                else if (s.detail == null || s.detail!.isEmpty)
                  const Text('[no output]', style: T.metaFaint),
              ],
            ),
          ),
      ],
    );
  }
}

class EditDiffView extends StatelessWidget {
  const EditDiffView(this.edit, {super.key});
  final EditInfo edit;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            FlatHeader(
              title: edit.isNew ? 'New file' : 'Edit',
              leading: CircleIconButton(
                  icon: Icons.close, onTap: () => backOrHome(context)),
            ),
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 4, 20, 4),
              child: Text('File', style: T.meta),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: T.surface,
                  borderRadius: BorderRadius.circular(10),
                  border: Border.all(color: T.border),
                ),
                child: Text(edit.file, style: T.mono.copyWith(color: T.text)),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 4, 20, 4),
              child: Text(edit.isNew ? 'Contents' : 'Output', style: T.meta),
            ),
            Expanded(child: EditDiffLines(edit)),
          ],
        ),
      ),
    );
  }
}

/// The lines of an edit, without the page around them.
///
/// Two callers want the same thing drawn differently: a phone opens a page for
/// it, where the diff has room; a desktop window has the room already, and
/// pushing a whole view over the conversation to read four lines of go.mod
/// loses the place you were reading.
class EditDiffLines extends StatelessWidget {
  const EditDiffLines(this.edit, {super.key, this.inline = false});

  final EditInfo edit;

  /// Laid out to its full height so the transcript scrolls it, rather than
  /// scrolling inside a box inside a scroll.
  final bool inline;

  @override
  Widget build(BuildContext context) {
    // New file: show its contents plainly (line-numbered), not a diff.
    final lines = (edit.isNew ? edit.content : edit.diff)
        .replaceAll('\r', '')
        .split('\n');
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: ListView.builder(
        shrinkWrap: inline,
        physics: inline ? const NeverScrollableScrollPhysics() : null,
        itemCount: lines.length,
        itemBuilder: (_, i) => edit.isNew
            ? _contentLine(i + 1, lines[i])
            : _diffLine(i + 1, lines[i]),
      ),
    );
  }

  /// A plain line-numbered row for new-file contents (no diff coloring).
  Widget _contentLine(int n, String line) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 44,
            child: Text('$n',
                textAlign: TextAlign.right,
                style: T.mono.copyWith(color: T.textFaint)),
          ),
          const SizedBox(width: 12),
          Text(line, style: T.mono.copyWith(color: T.text, height: 1.4)),
        ],
      ),
    );
  }

  Widget _diffLine(int n, String line) {
    Color bg = Colors.transparent;
    Color fg = T.textDim;
    if (line.startsWith('+')) {
      bg = const Color(0x2233A05B);
      fg = T.ok;
    } else if (line.startsWith('-')) {
      bg = const Color(0x22D9534F);
      fg = T.danger;
    } else if (line.startsWith('  ')) {
      fg = T.text;
    }
    return Container(
      color: bg,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 44,
            child: Text('$n',
                textAlign: TextAlign.right,
                style: T.mono.copyWith(color: T.textFaint)),
          ),
          const SizedBox(width: 12),
          Text(line, style: T.mono.copyWith(color: fg, height: 1.4)),
        ],
      ),
    );
  }
}

/// The steps behind a summary line, as a sheet over the conversation.
///
/// They used to expand in place, and on a phone that was the wrong move twice
/// over: a run of fifteen calls pushed the conversation off the screen, and
/// every row had to be clipped to one line to fit the column it was expanding
/// inside — "Let me check what…", "Ran go get github.…", a list of truncations.
///
/// A sheet has the width to say the whole line and closes without losing your
/// place in the transcript. Which is where Claude Code puts it, for what look
/// like the same two reasons.
Future<void> showActivitySheet(BuildContext context, Activity a) =>
    showResponsiveModal<void>(
      context: context,
      maxWidth: 560,
      builder: (_, dialog) => _ActivitySheet(activity: a, dialog: dialog),
    );

class _ActivitySheet extends StatelessWidget {
  const _ActivitySheet({required this.activity, this.dialog = false});
  final Activity activity;
  final bool dialog;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        constraints:
            BoxConstraints(maxHeight: MediaQuery.of(context).size.height * 0.8),
        decoration: modalDecoration(dialog),
        child: ListView(
          padding: const EdgeInsets.all(16),
          shrinkWrap: true,
          children: [
            Row(children: [
              CircleIconButton(
                  icon: Icons.close, onTap: () => Navigator.pop(context)),
              Expanded(
                  child: Text(activity.summary,
                      style: T.title, textAlign: TextAlign.center)),
              const SizedBox(width: 44),
            ]),
            const SizedBox(height: 12),
            for (final s in activity.steps)
              if (s.aside)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(s.label,
                      style: metaFor(context).copyWith(color: T.textDim)),
                )
              else
                _InlineStep(s),
          ],
        ),
      ),
    );
  }
}
