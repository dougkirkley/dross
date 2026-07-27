import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Built-in fallback presets per provider, used when neither a live model list
/// nor a configured allow-list is available.
const _defaults = {
  ProviderKind.anthropic: ['claude-opus-5', 'claude-sonnet-5', 'claude-haiku-4-5'],
  ProviderKind.openai: ['qwen3-coder:30b', 'llama3.1:8b', 'deepseek-coder-v2:16b'],
};

const _labels = {
  ProviderKind.anthropic: 'Anthropic',
  ProviderKind.openai: 'OpenAI-compatible',
};

/// Bottom sheet to change the active model, grouped by provider. For the
/// OpenAI-compatible provider it lists the models the endpoint actually serves
/// (unavailable ones are never offered); Anthropic uses the configured allow-list
/// or presets. A free-text field remains for arbitrary ids.
Future<void> showModelSheet(BuildContext context) {
  final s = context.read<AppState>();
  final ctrl = TextEditingController(text: s.currentModel);

  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
      child: _ModelSheet(state: s, ctrl: ctrl),
    ),
  );
}

class _ModelSheet extends StatefulWidget {
  const _ModelSheet({required this.state, required this.ctrl});
  final AppState state;
  final TextEditingController ctrl;

  @override
  State<_ModelSheet> createState() => _ModelSheetState();
}

class _ModelSheetState extends State<_ModelSheet> {
  List<String>? _liveOpenAI; // null = still loading

  @override
  void initState() {
    super.initState();
    widget.state.availableOpenAIModels().then((ids) {
      if (mounted) setState(() => _liveOpenAI = ids);
    });
  }

  List<String> _modelsFor(ProviderKind k) {
    if (k == ProviderKind.openai && (_liveOpenAI?.isNotEmpty ?? false)) {
      return _liveOpenAI!;
    }
    final allowed = widget.state.modelsFor(k);
    return allowed.isNotEmpty ? allowed : _defaults[k]!;
  }

  @override
  Widget build(BuildContext context) {
    final s = widget.state;
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
                    child: Text('Model',
                        style: T.title, textAlign: TextAlign.center)),
                const SizedBox(width: 44),
              ]),
              const SizedBox(height: 8),
              for (final kind in ProviderKind.values) ...[
                const SizedBox(height: 10),
                Row(children: [
                  Text(_labels[kind]!, style: T.metaFaint),
                  if (kind == ProviderKind.openai && _liveOpenAI == null) ...[
                    const SizedBox(width: 8),
                    const SizedBox(
                        width: 12,
                        height: 12,
                        child: CircularProgressIndicator(
                            strokeWidth: 1.5, color: T.textFaint)),
                  ],
                ]),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final m in _modelsFor(kind))
                      OutlineChip(
                        filled: s.providerKind == kind && ctrl.text == m,
                        onTap: () => setState(() {
                          s.setProviderModel(kind, m);
                          ctrl.text = m;
                        }),
                        child: Text(m,
                            style: const TextStyle(color: T.text, fontSize: 13)),
                      ),
                  ],
                ),
              ],
              const SizedBox(height: 16),
              Text('Custom (${_labels[s.providerKind]})', style: T.metaFaint),
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
                  s.setModel(ctrl.text);
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
