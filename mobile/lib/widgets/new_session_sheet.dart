import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';

/// Choice returned by the new-session sheet: a lead model, and an optional
/// worker model to delegate mechanical subtasks to.
typedef NewSessionChoice = ({String model, String? worker});

/// Bottom sheet shown before creating a remote (Code) session, to pick the lead
/// model and an optional worker (delegate) model. Returns null if cancelled.
Future<NewSessionChoice?> showNewSessionSheet(BuildContext context) {
  final s = context.read<AppState>();
  return showModalBottomSheet<NewSessionChoice>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
      child: _Sheet(state: s),
    ),
  );
}

class _Sheet extends StatefulWidget {
  const _Sheet({required this.state});
  final AppState state;
  @override
  State<_Sheet> createState() => _SheetState();
}

class _SheetState extends State<_Sheet> {
  List<String>? _models; // null = loading
  String? _model;
  String? _worker; // null = none

  @override
  void initState() {
    super.initState();
    widget.state.availableServerModels().then((m) {
      if (!mounted) return;
      setState(() {
        _models = m;
        if (m.isNotEmpty) _model = m.first;
      });
    });
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        decoration: const BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
          border: Border(top: BorderSide(color: T.border)),
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(children: [
                CircleIconButton(
                    icon: Icons.close, onTap: () => Navigator.pop(context)),
                const Expanded(
                    child: Text('New session',
                        style: T.title, textAlign: TextAlign.center)),
                const SizedBox(width: 44),
              ]),
              const SizedBox(height: 12),
              if (_models == null)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 16),
                  child: Center(
                      child: SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                              strokeWidth: 1.5, color: T.textFaint))),
                )
              else ...[
                const Text('Model', style: T.metaFaint),
                const SizedBox(height: 8),
                Wrap(spacing: 8, runSpacing: 8, children: [
                  for (final m in _models!)
                    OutlineChip(
                      filled: _model == m,
                      onTap: () => setState(() => _model = m),
                      child: Text(m,
                          style: const TextStyle(color: T.text, fontSize: 13)),
                    ),
                ]),
                const SizedBox(height: 16),
                const Text('Delegate to (optional)', style: T.metaFaint),
                const SizedBox(height: 8),
                Wrap(spacing: 8, runSpacing: 8, children: [
                  OutlineChip(
                    filled: _worker == null,
                    onTap: () => setState(() => _worker = null),
                    child: const Text('None',
                        style: TextStyle(color: T.text, fontSize: 13)),
                  ),
                  for (final m in _models!)
                    OutlineChip(
                      filled: _worker == m,
                      onTap: () => setState(() => _worker = m),
                      child: Text(m,
                          style: const TextStyle(color: T.text, fontSize: 13)),
                    ),
                ]),
              ],
              const SizedBox(height: 20),
              GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: _model == null
                    ? null
                    : () => Navigator.pop(
                        context, (model: _model!, worker: _worker)),
                child: Container(
                  padding: const EdgeInsets.symmetric(vertical: 15),
                  alignment: Alignment.center,
                  decoration: BoxDecoration(
                    color: _model == null ? T.surfaceAlt : T.pill,
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
                  child: Text('Create session',
                      style: TextStyle(
                          color: _model == null ? T.textFaint : T.pillText,
                          fontSize: 16,
                          fontWeight: FontWeight.w600)),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
