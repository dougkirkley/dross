import 'package:flutter/material.dart';

import '../models.dart';
import '../theme.dart';
import 'modal.dart';

String _fmtRuntime(Duration d) {
  final s = d.inSeconds;
  if (s < 60) return '${s}s';
  final m = s ~/ 60;
  final rem = s % 60;
  if (m < 60) return '${m}m ${rem}s';
  final h = m ~/ 60;
  return '${h}h ${m % 60}m';
}

/// Bottom sheet listing background shells for the session: a Running group and a
/// collapsible Finished group. Tapping a task opens its detail.
Future<void> showBackgroundTasksSheet(
    BuildContext context, List<ShellTask> shells) {
  final running = shells.where((s) => s.running).toList();
  final finished = shells.where((s) => !s.running).toList();
  return showResponsiveModal<void>(
    context: context,
    maxWidth: 640, // shell output is wide; a 480 column wraps every line
    builder: (_, dialog) =>
        _Sheet(running: running, finished: finished, dialog: dialog),
  );
}

class _Sheet extends StatefulWidget {
  const _Sheet(
      {required this.running, required this.finished, this.dialog = false});
  final List<ShellTask> running;
  final List<ShellTask> finished;
  final bool dialog;
  @override
  State<_Sheet> createState() => _SheetState();
}

class _SheetState extends State<_Sheet> {
  bool _showFinished = true;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.8),
        decoration: modalDecoration(widget.dialog),
        child: ListView(
          padding: const EdgeInsets.all(16),
          shrinkWrap: true,
          children: [
            Row(children: [
              CircleIconButton(
                  icon: Icons.close, onTap: () => Navigator.pop(context)),
              const Expanded(
                  child: Text('Background tasks',
                      style: T.title, textAlign: TextAlign.center)),
              const SizedBox(width: 44),
            ]),
            const SizedBox(height: 16),
            if (widget.running.isNotEmpty) ...[
              const Text('Running', style: T.metaFaint),
              const SizedBox(height: 8),
              for (final s in widget.running) _card(context, s),
            ],
            if (widget.finished.isNotEmpty) ...[
              const SizedBox(height: 16),
              GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => setState(() => _showFinished = !_showFinished),
                child: Row(children: [
                  Text('Finished ${widget.finished.length}',
                      style: T.metaFaint),
                  Icon(_showFinished ? Icons.expand_less : Icons.expand_more,
                      size: 18, color: T.textFaint),
                ]),
              ),
              const SizedBox(height: 8),
              if (_showFinished)
                for (final s in widget.finished) _card(context, s),
            ],
            if (widget.running.isEmpty && widget.finished.isEmpty)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 24),
                child: Text('No background tasks.',
                    style: T.metaFaint, textAlign: TextAlign.center),
              ),
          ],
        ),
      ),
    );
  }

  Widget _card(BuildContext context, ShellTask s) => Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => _openDetail(context, s),
          child: Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: T.bg,
              borderRadius: BorderRadius.circular(T.rCard),
            ),
            child: Row(children: [
              const Icon(Icons.terminal, size: 18, color: T.textDim),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(s.command,
                        style: T.rowTitle,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                    const SizedBox(height: 4),
                    Text(
                      s.running
                          ? 'Shell   ${_fmtRuntime(s.runtime)}'
                          : 'Shell   ${s.exit.isEmpty ? "done" : s.exit}',
                      style: T.metaFaint,
                    ),
                  ],
                ),
              ),
              Icon(s.running ? Icons.stop_circle_outlined : Icons.chevron_right,
                  size: 20, color: T.textFaint),
            ]),
          ),
        ),
      );

  void _openDetail(BuildContext context, ShellTask s) {
    showResponsiveModal<void>(
      context: context,
      maxWidth: 640,
      builder: (_, dialog) => SafeArea(
        child: Container(
          constraints: BoxConstraints(
              maxHeight: MediaQuery.of(context).size.height * 0.85),
          decoration: modalDecoration(dialog),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.all(16),
                child: Row(children: [
                  CircleIconButton(
                      icon: Icons.chevron_left,
                      onTap: () => Navigator.pop(context)),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      s.running
                          ? 'Running · ${_fmtRuntime(s.runtime)}'
                          : '${s.exit.isEmpty ? "Done" : s.exit} · ${_fmtRuntime(s.runtime)}',
                      style: T.title,
                    ),
                  ),
                ]),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text('\$ ${s.command}',
                    style: T.mono.copyWith(color: T.text)),
              ),
              const SizedBox(height: 12),
              const Divider(height: 1, color: T.border),
              Flexible(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.all(16),
                  child: SelectableText(
                    s.output.isEmpty ? '[no output yet]' : s.output,
                    style: T.mono.copyWith(color: T.textDim, height: 1.4),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
