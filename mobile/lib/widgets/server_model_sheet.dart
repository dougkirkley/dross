import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import 'modal.dart';

/// Change the model of the current session: the models the server offers, a
/// free-text field for an arbitrary id, and which of them is loaded right now.
///
/// A bottom sheet on a phone, a centered dialog on a desktop width. The sheet
/// is a thumb idiom — it exists because that is where a thumb is. A pointer is
/// wherever the chip that opened this is, and a panel sliding up from the foot
/// of a large window is nowhere near it.
Future<void> showServerModelSheet(BuildContext context) {
  final s = context.read<AppState>();
  final ctrl = TextEditingController(text: s.current?.model ?? '');
  return showResponsiveModal<void>(
    context: context,
    builder: (_, dialog) => _Sheet(state: s, ctrl: ctrl, dialog: dialog),
  );
}

class _Sheet extends StatefulWidget {
  const _Sheet({required this.state, required this.ctrl, this.dialog = false});
  final AppState state;
  final TextEditingController ctrl;

  /// Rounded on all corners and bordered all round, rather than shaped like
  /// something docked to the bottom edge it no longer touches.
  final bool dialog;
  @override
  State<_Sheet> createState() => _SheetState();
}

class _SheetState extends State<_Sheet> {
  ModelChoices? _choices; // null = still asking the server

  @override
  void initState() {
    super.initState();
    widget.state.availableServerModels().then((m) {
      if (mounted) setState(() => _choices = m);
    });
  }

  /// Confirm a change that would unload a model somebody else may be mid-answer
  /// on.
  ///
  /// Not a warning about the person's own wait — that is theirs to accept — but
  /// about everyone else's. These backends hold one model at a time, so picking
  /// a cold one takes the warm one away from every other session pointed at
  /// that server, and nothing about the picker said so.
  Future<bool> _confirmEviction(String picked, String loaded) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (dctx) => AlertDialog(
        backgroundColor: T.surface,
        title: const Text('Swap the loaded model?', style: T.title),
        content: Text(
          '$loaded is loaded now. Switching to $picked unloads it for everyone '
          'using this server, and loading $picked takes a minute or two.',
          style: T.metaFaint,
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(dctx, false),
              child: const Text('Cancel', style: TextStyle(color: T.textDim))),
          TextButton(
              onPressed: () => Navigator.pop(dctx, true),
              child: const Text('Switch', style: TextStyle(color: T.accent))),
        ],
      ),
    );
    return ok == true;
  }

  @override
  Widget build(BuildContext context) {
    final ctrl = widget.ctrl;
    return SafeArea(
      child: Container(
        decoration: modalDecoration(widget.dialog),
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
              if (_choices == null)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 12),
                  child: Center(
                      child: SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                              strokeWidth: 1.5, color: T.textFaint))),
                )
              else if (_choices!.models.isEmpty)
                const Text('No models advertised by the server.',
                    style: T.metaFaint)
              else
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final m in _choices!.models)
                      OutlineChip(
                        filled: ctrl.text == m,
                        onTap: () => setState(() => ctrl.text = m),
                        // A dot on the ones that are warm. The cost of picking
                        // a model is entirely invisible otherwise: they all
                        // look alike, and one of them answers immediately while
                        // the rest cost a minute and take the loaded one away
                        // from whoever is using it.
                        child: Row(mainAxisSize: MainAxisSize.min, children: [
                          if (_choices!.isLoaded(m)) ...[
                            const Icon(Icons.circle, size: 7, color: T.ok),
                            const SizedBox(width: 6),
                          ],
                          Text(m,
                              style:
                                  const TextStyle(color: T.text, fontSize: 13)),
                        ]),
                      ),
                  ],
                ),
              if (_choices != null && _choices!.loaded.isNotEmpty) ...[
                const SizedBox(height: 10),
                Row(children: [
                  const Icon(Icons.circle, size: 7, color: T.ok),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                        'Loaded now: ${_choices!.loaded.join(', ')}. '
                        'Anything else takes a minute or two to load.',
                        style: T.metaFaint),
                  ),
                ]),
              ],
              const SizedBox(height: 16),
              const Text('Custom', style: T.metaFaint),
              const SizedBox(height: 8),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
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
                onTap: () async {
                  final picked = ctrl.text.trim();
                  final choices = _choices;
                  if (picked.isNotEmpty &&
                      choices != null &&
                      choices.evicts(picked) &&
                      !await _confirmEviction(picked, choices.loaded.first)) {
                    return;
                  }
                  if (!context.mounted) return;
                  widget.state.setSessionModel(picked);
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
