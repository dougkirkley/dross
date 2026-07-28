import 'dart:convert';

import 'package:flutter/material.dart';

import '../models.dart';
import '../theme.dart';

/// One step within an activity block: a tool call or a file edit.
class ActStep {
  ActStep({required this.icon, required this.label, this.color = T.text, this.edit});
  final IconData icon;
  final String label;
  final Color color;
  final EditInfo? edit;
}

/// A run of consecutive tool/edit events between assistant messages, shown
/// collapsed as one line ("Used a tool, edited a file +A -B") that expands.
class Activity {
  final List<ActStep> steps = [];

  int get added => steps.fold(0, (s, e) => s + (e.edit?.added ?? 0));
  int get removed => steps.fold(0, (s, e) => s + (e.edit?.removed ?? 0));
  int get edits => steps.where((s) => s.edit != null).length;
  int get tools => steps.where((s) => s.edit == null).length;

  /// Enumerates the distinct actions taken, like Claude's "Edited a file, ran a
  /// command, read a file".
  String get summary {
    final counts = <String, int>{};
    for (final s in steps) {
      final k = s.edit != null ? 'edit' : _verbKey(s.label);
      counts[k] = (counts[k] ?? 0) + 1;
    }
    const order = ['edit', 'bash', 'read', 'search', 'delegate', 'publish', 'tool'];
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
        return n == 1 ? 'ran a command' : 'ran $n commands';
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
List<Object> buildTimeline(List<Event> events) {
  final out = <Object>[];
  Activity? cur;
  void flush() {
    if (cur != null) {
      out.add(cur!);
      cur = null;
    }
  }

  for (final e in events) {
    if (e.isShell) continue; // background shells render in the tasks sheet
    switch (e.baseKind) {
      case 'prompt':
      case 'text':
      case 'review':
      case 'error':
      case 'checkpoint':
        flush();
        out.add(e);
      case 'done':
        flush(); // end the activity run; no visible "done" line
        break;
      case 'tool':
        final step = _toolStep(e.data);
        if (step != null) (cur ??= Activity()).steps.add(step);
      case 'tool-result':
      case 'usage': // token counter, not an activity step
      case 'answer':
        break; // not shown as a step
      case 'compact':
        (cur ??= Activity()).steps.add(ActStep(
            icon: Icons.compress,
            label: 'Compacted ${e.data}',
            color: T.textFaint));
      case 'edit':
        try {
          final info =
              EditInfo.fromJson(jsonDecode(e.data) as Map<String, dynamic>);
          (cur ??= Activity()).steps.add(ActStep(
              icon: Icons.edit_outlined,
              label: 'Edited ${info.file}',
              edit: info));
        } catch (_) {}
      default:
        (cur ??= Activity())
            .steps
            .add(ActStep(icon: Icons.bolt, label: e.data, color: T.accentHi));
    }
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
      return ActStep(
          icon: Icons.description_outlined, label: 'Read ${_argPath(arg)}');
    case 'bash':
      final cmd = _argField(arg, 'command');
      return ActStep(
          icon: Icons.terminal,
          label: cmd.isEmpty ? 'Ran a command' : 'Ran  ${_trim(cmd)}');
    case 'delegate':
      final task = _argField(arg, 'task');
      return ActStep(
          icon: Icons.call_split,
          label: task.isEmpty
              ? 'Delegated to worker'
              : 'Delegated  ${_trim(task)}');
    case 'publish':
      return ActStep(icon: Icons.link, label: 'Published an artifact');
    case 'web_search':
    case 'web-search':
      return ActStep(icon: Icons.search, label: 'Searched the web');
    default:
      return ActStep(icon: Icons.build_outlined, label: 'Used $name');
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
class ActivityBlock extends StatelessWidget {
  const ActivityBlock(this.activity, {super.key});
  final Activity activity;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => _showSteps(context, activity),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(
          children: [
            Flexible(
              child: Text(activity.summary,
                  style: T.meta, overflow: TextOverflow.ellipsis),
            ),
            if (activity.added > 0 || activity.removed > 0) ...[
              const SizedBox(width: 8),
              Text('+${activity.added}',
                  style: const TextStyle(color: T.ok, fontSize: 13)),
              const SizedBox(width: 4),
              Text('-${activity.removed}',
                  style: const TextStyle(color: T.danger, fontSize: 13)),
            ],
            const SizedBox(width: 4),
            const Icon(Icons.chevron_right, size: 18, color: T.textFaint),
          ],
        ),
      ),
    );
  }
}

void _showSteps(BuildContext context, Activity a) {
  showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(children: [
              CircleIconButton(
                  icon: Icons.close, onTap: () => Navigator.pop(context)),
              Expanded(
                  child: Text(a.summary,
                      style: T.title, textAlign: TextAlign.center)),
              const SizedBox(width: 44),
            ]),
            const SizedBox(height: 16),
            Container(
              decoration: BoxDecoration(
                color: T.surface,
                borderRadius: BorderRadius.circular(T.rCard),
                border: Border.all(color: T.border),
              ),
              child: Column(
                children: [
                  for (var i = 0; i < a.steps.length; i++) ...[
                    _stepRow(context, a.steps[i]),
                    if (i != a.steps.length - 1)
                      const Divider(
                          height: 1, color: T.borderFaint, indent: 52),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

Widget _stepRow(BuildContext context, ActStep s) {
  final edit = s.edit;
  return InkWell(
    onTap: edit == null
        ? null
        : () => Navigator.push(context,
            MaterialPageRoute(builder: (_) => EditDiffView(edit))),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      child: Row(
        children: [
          Icon(s.icon, size: 20, color: s.color),
          const SizedBox(width: 14),
          Expanded(child: Text(s.label, style: T.body)),
          if (edit != null) ...[
            Text('+${edit.added}', style: const TextStyle(color: T.ok, fontSize: 13)),
            const SizedBox(width: 4),
            Text('-${edit.removed}',
                style: const TextStyle(color: T.danger, fontSize: 13)),
            const SizedBox(width: 6),
            const Icon(Icons.chevron_right, size: 18, color: T.textFaint),
          ],
        ],
      ),
    ),
  );
}

/// Full diff view for one edit: file path + the unified diff with green/red
/// lines and a line-number gutter.
class EditDiffView extends StatelessWidget {
  const EditDiffView(this.edit, {super.key});
  final EditInfo edit;

  @override
  Widget build(BuildContext context) {
    final lines = edit.diff.split('\n');
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            FlatHeader(
              title: 'Edit',
              leading: CircleIconButton(
                  icon: Icons.close, onTap: () => Navigator.pop(context)),
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
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 4, 20, 4),
              child: Text('Output', style: T.meta),
            ),
            Expanded(
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: ListView.builder(
                  shrinkWrap: false,
                  itemCount: lines.length,
                  itemBuilder: (_, i) => _diffLine(i + 1, lines[i]),
                ),
              ),
            ),
          ],
        ),
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
