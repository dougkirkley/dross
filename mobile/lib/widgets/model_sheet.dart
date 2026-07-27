import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Built-in fallback presets per provider, used when Settings defines no
/// allowed-models list for that provider.
const _defaults = {
  ProviderKind.anthropic: ['claude-opus-5', 'claude-sonnet-5', 'claude-haiku-4-5'],
  ProviderKind.openai: ['qwen3-coder:30b', 'llama3.1:8b', 'deepseek-coder-v2:16b'],
};

const _labels = {
  ProviderKind.anthropic: 'Anthropic',
  ProviderKind.openai: 'OpenAI-compatible',
};

/// Bottom sheet to change the active model, grouped by provider. Tapping a
/// model selects both its provider and the model. A free-text field still lets
/// you enter an arbitrary id for the current provider.
Future<void> showModelSheet(BuildContext context) {
  final s = context.read<AppState>();
  final ctrl = TextEditingController(text: s.currentModel);

  List<String> modelsFor(ProviderKind k) {
    final allowed = s.modelsFor(k);
    return allowed.isNotEmpty ? allowed : _defaults[k]!;
  }

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
                const SizedBox(height: 8),
                for (final kind in ProviderKind.values) ...[
                  const SizedBox(height: 10),
                  Text(_labels[kind]!, style: T.metaFaint),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final m in modelsFor(kind))
                        OutlineChip(
                          filled: s.providerKind == kind && ctrl.text == m,
                          onTap: () => setSheet(() {
                            s.setProviderModel(kind, m);
                            ctrl.text = m;
                          }),
                          child: Text(m,
                              style:
                                  const TextStyle(color: T.text, fontSize: 13)),
                        ),
                    ],
                  ),
                ],
                const SizedBox(height: 16),
                Text('Custom (${_labels[s.providerKind]})', style: T.metaFaint),
                const SizedBox(height: 8),
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
