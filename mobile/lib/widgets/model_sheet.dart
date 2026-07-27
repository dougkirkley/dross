import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Bottom sheet to change the model for the active direct provider — tapped from
/// the model chip in the chat input. Offers presets plus a free-text field.
Future<void> showModelSheet(BuildContext context) {
  final s = context.read<AppState>();
  final ctrl = TextEditingController(text: s.currentModel);
  final presets = s.providerKind == ProviderKind.anthropic
      ? const ['claude-opus-5', 'claude-sonnet-5', 'claude-haiku-4-5']
      : const ['qwen3-coder:30b', 'llama3.1:8b', 'deepseek-coder-v2:16b'];

  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
      child: SafeArea(
        child: StatefulBuilder(
          builder: (ctx, setSheet) => Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(children: [
                  CircleIconButton(
                      icon: Icons.close, onTap: () => Navigator.pop(ctx)),
                  const Expanded(
                      child: Text('Model',
                          style: T.title, textAlign: TextAlign.center)),
                  const SizedBox(width: 44),
                ]),
                const SizedBox(height: 16),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final p in presets)
                      OutlineChip(
                        filled: ctrl.text == p,
                        onTap: () => setSheet(() => ctrl.text = p),
                        child: Text(p,
                            style: const TextStyle(color: T.text, fontSize: 13)),
                      ),
                  ],
                ),
                const SizedBox(height: 16),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
                  decoration: BoxDecoration(
                    color: T.surface,
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
                    context.read<AppState>().setModel(ctrl.text);
                    Navigator.pop(ctx);
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
      ),
    ),
  );
}
