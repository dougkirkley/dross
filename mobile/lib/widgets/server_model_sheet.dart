import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';

/// Bottom sheet to change the model of the current remote (code) session. Lists
/// the models the server offers, plus a free-text field for an arbitrary id.
Future<void> showServerModelSheet(BuildContext context) {
  final s = context.read<AppState>();
  final ctrl = TextEditingController(text: s.current?.model ?? '');
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
      child: _Sheet(state: s, ctrl: ctrl),
    ),
  );
}

class _Sheet extends StatefulWidget {
  const _Sheet({required this.state, required this.ctrl});
  final AppState state;
  final TextEditingController ctrl;
  @override
  State<_Sheet> createState() => _SheetState();
}

class _SheetState extends State<_Sheet> {
  List<String>? _models; // null = loading

  @override
  void initState() {
    super.initState();
    widget.state.availableServerModels().then((m) {
      if (mounted) setState(() => _models = m);
    });
  }

  @override
  Widget build(BuildContext context) {
    final ctrl = widget.ctrl;
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
                    child: Text('Session model',
                        style: T.title, textAlign: TextAlign.center)),
                const SizedBox(width: 44),
              ]),
              const SizedBox(height: 12),
              if (_models == null)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 12),
                  child: Center(
                      child: SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                              strokeWidth: 1.5, color: T.textFaint))),
                )
              else if (_models!.isEmpty)
                const Text('No models advertised by the server.',
                    style: T.metaFaint)
              else
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final m in _models!)
                      OutlineChip(
                        filled: ctrl.text == m,
                        onTap: () => setState(() => ctrl.text = m),
                        child: Text(m,
                            style: const TextStyle(color: T.text, fontSize: 13)),
                      ),
                  ],
                ),
              const SizedBox(height: 16),
              const Text('Custom', style: T.metaFaint),
              const SizedBox(height: 8),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
                decoration: BoxDecoration(
                  color: T.bg,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: T.border),
                ),
                child: TextField(
                  controller: ctrl,
                  autocorrect: false,
                  style: T.body,
                  decoration: const InputDecoration.collapsed(
                      hintText: 'model id',
                      hintStyle: TextStyle(color: T.textFaint)),
                ),
              ),
              const SizedBox(height: 16),
              GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () {
                  widget.state.setSessionModel(ctrl.text);
                  Navigator.pop(context);
                },
                child: Container(
                  padding: const EdgeInsets.symmetric(vertical: 15),
                  alignment: Alignment.center,
                  decoration: BoxDecoration(
                    color: T.pill,
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
                  child: const Text('Use model',
                      style: TextStyle(
                          color: T.pillText,
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
