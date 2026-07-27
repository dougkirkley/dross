import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Provider + connection settings. Direct mode (default) talks straight to a
/// provider; Remote mode attaches to `holocron serve`. Mirrors the same info
/// the desktop client reads from env / config.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});
  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late AppState s;
  late ConnMode _mode;
  late ProviderKind _provider;

  final _openaiUrl = TextEditingController();
  final _openaiKey = TextEditingController();
  final _openaiModel = TextEditingController();
  final _anthUrl = TextEditingController();
  final _anthKey = TextEditingController();
  final _anthModel = TextEditingController();
  final _identity = TextEditingController();
  final _serverUrl = TextEditingController();
  final _serverToken = TextEditingController();

  @override
  void initState() {
    super.initState();
    s = context.read<AppState>();
    _mode = s.mode;
    _provider = s.providerKind;
    _openaiUrl.text = s.openaiBaseUrl;
    _openaiKey.text = s.openaiKey;
    _openaiModel.text = s.openaiModel;
    _anthUrl.text = s.anthropicBaseUrl;
    _anthKey.text = s.anthropicKey;
    _anthModel.text = s.anthropicModel;
    _identity.text = s.identity;
    _serverUrl.text = s.baseUrl;
    _serverToken.text = s.token;
  }

  @override
  void dispose() {
    for (final c in [
      _openaiUrl,
      _openaiKey,
      _openaiModel,
      _anthUrl,
      _anthKey,
      _anthModel,
      _identity,
      _serverUrl,
      _serverToken,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            FlatHeader(
              title: 'Settings',
              leading: CircleIconButton(
                  icon: Icons.chevron_left,
                  onTap: () => Navigator.pop(context)),
            ),
            Expanded(
              child: Center(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 560),
                  child: ListView(
                    padding: const EdgeInsets.fromLTRB(20, 8, 20, 40),
                    children: [
                  _label('Connection'),
                  _toggle<ConnMode>(
                    value: _mode,
                    options: const {
                      ConnMode.direct: 'Direct',
                      ConnMode.remote: 'Remote (serve)',
                    },
                    onChanged: (v) => setState(() => _mode = v),
                  ),
                  const SizedBox(height: 24),
                  if (_mode == ConnMode.direct) ..._directFields(),
                  if (_mode == ConnMode.remote) ..._remoteFields(),
                  const SizedBox(height: 28),
                  GestureDetector(
                    onTap: _save,
                    child: Container(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: T.pill,
                        borderRadius: BorderRadius.circular(T.rPill),
                      ),
                      child: const Text('Save',
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
          ],
        ),
      ),
    );
  }

  List<Widget> _directFields() => [
        _label('Provider'),
        _toggle<ProviderKind>(
          value: _provider,
          options: const {
            ProviderKind.openai: 'OpenAI-compatible',
            ProviderKind.anthropic: 'Anthropic',
          },
          onChanged: (v) => setState(() => _provider = v),
        ),
        const SizedBox(height: 20),
        if (_provider == ProviderKind.openai) ...[
          _field('Base URL', _openaiUrl,
              hint: 'https://ollama.example.com/v1'),
          _field('API key', _openaiKey,
              hint: 'optional for local ollama', secret: true),
          _field('Model', _openaiModel, hint: 'qwen3-coder:30b'),
        ] else ...[
          _field('Base URL', _anthUrl, hint: 'https://api.anthropic.com'),
          _field('API key', _anthKey, hint: 'sk-ant-…', secret: true),
          _field('Model', _anthModel, hint: 'claude-opus-5'),
        ],
        const SizedBox(height: 12),
        _field('Identity / system prompt', _identity,
            hint: 'optional', lines: 3),
      ];

  List<Widget> _remoteFields() => [
        _label('holocron serve'),
        _field('Server URL', _serverUrl,
            hint: 'https://holocron.example.com'),
        _field('Bearer token', _serverToken,
            hint: 'from your SSO / static token', secret: true),
      ];

  Widget _label(String t) => Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: Text(t, style: T.meta),
      );

  Widget _toggle<E>({
    required E value,
    required Map<E, String> options,
    required ValueChanged<E> onChanged,
  }) =>
      Container(
        padding: const EdgeInsets.all(4),
        decoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rPill),
          border: Border.all(color: T.border),
        ),
        child: Row(
          children: [
            for (final e in options.entries)
              Expanded(
                child: GestureDetector(
                  onTap: () => onChanged(e.key),
                  child: Container(
                    padding: const EdgeInsets.symmetric(vertical: 9),
                    alignment: Alignment.center,
                    decoration: BoxDecoration(
                      color: e.key == value ? T.accent : Colors.transparent,
                      borderRadius: BorderRadius.circular(T.rPill),
                    ),
                    child: Text(e.value,
                        style: TextStyle(
                            color: e.key == value ? T.bg : T.textDim,
                            fontWeight: FontWeight.w600,
                            fontSize: 14)),
                  ),
                ),
              ),
          ],
        ),
      );

  Widget _field(String label, TextEditingController c,
          {String? hint, bool secret = false, int lines = 1}) =>
      Padding(
        padding: const EdgeInsets.only(bottom: 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(label, style: T.metaFaint),
            const SizedBox(height: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
              decoration: BoxDecoration(
                color: T.surface,
                borderRadius: BorderRadius.circular(12),
                border: Border.all(color: T.border),
              ),
              child: TextField(
                controller: c,
                obscureText: secret,
                autocorrect: false,
                enableSuggestions: false,
                minLines: lines,
                maxLines: secret ? 1 : lines,
                style: T.body,
                decoration: InputDecoration.collapsed(
                    hintText: hint,
                    hintStyle: const TextStyle(color: T.textFaint)),
              ),
            ),
          ],
        ),
      );

  Future<void> _save() async {
    s.mode = _mode;
    s.providerKind = _provider;
    s.openaiBaseUrl = _openaiUrl.text.trim();
    s.openaiKey = _openaiKey.text.trim();
    s.openaiModel = _openaiModel.text.trim();
    s.anthropicBaseUrl = _anthUrl.text.trim();
    s.anthropicKey = _anthKey.text.trim();
    s.anthropicModel = _anthModel.text.trim();
    s.identity = _identity.text.trim();
    s.baseUrl = _serverUrl.text.trim().replaceAll(RegExp(r'/+$'), '');
    s.token = _serverToken.text.trim();
    await s.saveConfig();
    if (mounted) Navigator.pop(context);
  }
}
