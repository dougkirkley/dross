import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../util/file_pick.dart';
import '../widgets/activity.dart';
import '../widgets/logo.dart';
import '../widgets/server_model_sheet.dart';
import '../widgets/tasks_sheet.dart';
import 'chat_screen.dart' show ProseText;

/// Remote-session conversation. Streams the serve event log (tool calls,
/// delegation indented, cost) styled to match the app: user bubbles, serif
/// assistant prose, monospace tool lines.
class SessionScreen extends StatefulWidget {
  const SessionScreen({super.key});
  @override
  State<SessionScreen> createState() => _SessionScreenState();
}

class _SessionScreenState extends State<SessionScreen> {
  final _input = TextEditingController();
  final _scroll = ScrollController();

  @override
  void dispose() {
    _input.dispose();
    _scroll.dispose();
    super.dispose();
  }

  void _autoScroll() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(_scroll.position.maxScrollExtent,
            duration: const Duration(milliseconds: 180), curve: Curves.easeOut);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final sess = s.current;
    _autoScroll();
    return SelectionArea(
      child: Scaffold(
      backgroundColor: T.chatBg,
      body: SafeArea(
        child: Column(
          children: [
            FlatHeader(
              title: sess?.model ?? 'session',
              subtitle: '\$${(sess?.cost ?? 0).toStringAsFixed(4)}',
              leading: CircleIconButton(
                  icon: Icons.chevron_left,
                  onTap: () => Navigator.pop(context)),
              trailing: (sess?.isRunning ?? false)
                  ? CircleIconButton(
                      icon: Icons.stop,
                      tooltip: 'Interrupt',
                      onTap: () => context.read<AppState>().interrupt())
                  : null,
            ),
            Expanded(
              child: Builder(builder: (_) {
                final timeline = buildTimeline(s.events);
                final shells = buildShells(s.events);
                final runningShells =
                    shells.where((x) => x.running).length;
                // Trailing items after the conversation: the live spinner (while
                // the turn runs) and a "N running task" link (background shells).
                final trailing = <Widget>[
                  if (sess?.isRunning ?? false) _runningRow(s),
                  if (runningShells > 0) _tasksLink(context, shells, runningShells),
                ];
                return ListView.builder(
                  controller: _scroll,
                  padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
                  itemCount: timeline.length + trailing.length,
                  itemBuilder: (_, i) {
                    if (i >= timeline.length) return trailing[i - timeline.length];
                    final item = timeline[i];
                    if (item is Activity) return ActivityBlock(item);
                    return _eventRow(item as Event);
                  },
                );
              }),
            ),
            if (s.queue.isNotEmpty) _queueStrip(context, s),
            _inputBar(context, s),
          ],
        ),
      ),
    ));
  }

  Widget _tasksLink(BuildContext context, List<ShellTask> shells, int running) =>
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => showBackgroundTasksSheet(context, shells),
          child: Row(mainAxisSize: MainAxisSize.min, children: [
            const Text('✻ ', style: TextStyle(color: T.accentHi, fontSize: 15)),
            Text('$running running task${running == 1 ? '' : 's'}',
                style: const TextStyle(
                    color: T.accentHi,
                    fontSize: 15,
                    fontWeight: FontWeight.w600)),
          ]),
        ),
      );

  Widget _runningRow(AppState s) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(children: [
          const ErgonSpinner(size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(currentActivityLabel(s.events),
                style: T.meta, maxLines: 1, overflow: TextOverflow.ellipsis),
          ),
        ]),
      );

  Widget _queueStrip(BuildContext context, AppState s) => Container(
        margin: const EdgeInsets.fromLTRB(16, 0, 16, 4),
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rCard),
          border: Border.all(color: T.border),
        ),
        child: Column(
          children: [
            for (int i = 0; i < s.queue.length; i++)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 2),
                child: Row(children: [
                  const Icon(Icons.schedule, size: 15, color: T.textDim),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(s.queue[i],
                        style: T.meta,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                  ),
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: const Icon(Icons.arrow_upward,
                        size: 18, color: T.accent),
                    tooltip: 'Run now',
                    onPressed: () => context.read<AppState>().promoteRemote(i),
                  ),
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: const Icon(Icons.edit_outlined,
                        size: 18, color: T.textDim),
                    tooltip: 'Edit',
                    onPressed: () {
                      _input.text = context.read<AppState>().removeQueued(i);
                    },
                  ),
                ]),
              ),
          ],
        ),
      );

  Widget _eventRow(Event e) {
    final indent = e.isWorker ? const EdgeInsets.only(left: 20) : EdgeInsets.zero;
    Widget content;
    switch (e.baseKind) {
      case 'prompt':
        content = Align(
          alignment: Alignment.centerRight,
          child: Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            constraints: BoxConstraints(
                maxWidth: MediaQuery.of(context).size.width * 0.82),
            decoration: BoxDecoration(
                color: T.surfaceAlt,
                borderRadius: BorderRadius.circular(T.rBubble)),
            child: Text(e.data, style: T.body),
          ),
        );
        break;
      case 'text':
        content = ProseText(e.data);
        break;
      case 'review':
        content = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('code review', style: T.metaFaint),
            const SizedBox(height: 2),
            ProseText(e.data),
          ],
        );
        break;
      case 'tool':
        content = _mono('→ ${e.data}', T.accentHi);
        break;
      case 'tool-result':
        content = _mono(e.data, T.textFaint, max: 3);
        break;
      case 'checkpoint':
        content = _mono('✓ ${e.data}', T.accentHi);
        break;
      case 'error':
        content = _mono('✗ ${e.data}', T.danger);
        break;
      case 'done':
        content = _mono('done · ${e.data}', T.textFaint);
        break;
      default:
        content = _mono(e.data, T.textDim);
    }
    return Padding(
        padding: const EdgeInsets.symmetric(vertical: 6).add(indent),
        child: content);
  }

  Widget _mono(String text, Color color, {int? max}) => Text(
        text,
        maxLines: max,
        overflow: max != null ? TextOverflow.ellipsis : TextOverflow.clip,
        style: T.mono.copyWith(color: color),
      );

  Widget _inputBar(BuildContext context, AppState s) {
    final running = s.current?.isRunning ?? false;
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
        child: Container(
          padding: const EdgeInsets.fromLTRB(14, 6, 8, 6),
          decoration: BoxDecoration(
            color: T.inputBg,
            borderRadius: BorderRadius.circular(T.rCard),
            border: Border.all(color: T.border),
          ),
          child: Column(
            children: [
              if (s.remotePending.isNotEmpty) _attachChips(context, s),
              Row(children: [
                Expanded(
                  child: TextField(
                    controller: _input,
                    minLines: 1,
                    maxLines: 5,
                    style: T.body,
                    decoration: InputDecoration.collapsed(
                        hintText: running
                            ? 'Queue after this turn…'
                            : 'Build anything…',
                        hintStyle: const TextStyle(color: T.textDim)),
                    onSubmitted: (_) => _send(context),
                  ),
                ),
                GestureDetector(
                  onTap: () => _send(context),
                  child: Container(
                    width: 40,
                    height: 40,
                    decoration: const BoxDecoration(
                        color: T.accent, shape: BoxShape.circle),
                    child: Icon(running ? Icons.add : Icons.arrow_upward,
                        size: 20, color: T.bg),
                  ),
                ),
              ]),
              const SizedBox(height: 8),
              Row(children: [
                _circleAction(Icons.add, () => _pickFile(context)),
                const SizedBox(width: 8),
                Flexible(
                  child: OutlineChip(
                    onTap: () => showServerModelSheet(context),
                    child: Text(s.current?.model ?? 'model',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style:
                            const TextStyle(color: T.textDim, fontSize: 13)),
                  ),
                ),
                const Spacer(),
              ]),
            ],
          ),
        ),
      ),
    );
  }

  void _send(BuildContext context) {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    _input.clear();
    context.read<AppState>().sendPrompt(text);
  }

  Widget _circleAction(IconData icon, VoidCallback onTap) => GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          width: 36,
          height: 36,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: T.border),
          ),
          child: Icon(icon, size: 18, color: T.text),
        ),
      );

  Future<void> _pickFile(BuildContext context) async {
    final picked = await pickFile();
    if (picked == null || !context.mounted) return;
    final err = await context.read<AppState>().attachRemote(picked);
    if (err != null && context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Upload failed: $err')));
    }
  }

  Widget _attachChips(BuildContext context, AppState s) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Align(
          alignment: Alignment.centerLeft,
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (int i = 0; i < s.remotePending.length; i++)
                Container(
                  padding: const EdgeInsets.fromLTRB(10, 6, 6, 6),
                  decoration: BoxDecoration(
                    color: T.surfaceAlt,
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
                  child: Row(mainAxisSize: MainAxisSize.min, children: [
                    const Icon(Icons.attach_file, size: 15, color: T.textDim),
                    const SizedBox(width: 6),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 160),
                      child: Text(s.remotePending[i].name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(color: T.text, fontSize: 13)),
                    ),
                    GestureDetector(
                      onTap: () =>
                          context.read<AppState>().removeRemotePending(i),
                      child: const Padding(
                        padding: EdgeInsets.only(left: 4),
                        child: Icon(Icons.close, size: 16, color: T.textDim),
                      ),
                    ),
                  ]),
                ),
            ],
          ),
        ),
      );
}
