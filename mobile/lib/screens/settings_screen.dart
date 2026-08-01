import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';

/// Settings hold only the server URL — every agent/provider/permission setting
/// lives on the `dross serve` instance. Also offers sign out.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});
  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  final _serverUrl = TextEditingController();
  bool _saved = false;

  @override
  void initState() {
    super.initState();
    _serverUrl.text = context.read<AppState>().baseUrl;
  }

  @override
  void dispose() {
    _serverUrl.dispose();
    super.dispose();
  }

  static String _normalize(String s) {
    s = s.trim();
    if (s.isEmpty) return '';
    if (!s.startsWith('http://') && !s.startsWith('https://')) s = 'https://$s';
    return s.replaceAll(RegExp(r'/+$'), '');
  }

  Future<void> _save() async {
    await context.read<AppState>().setServer(_normalize(_serverUrl.text));
    if (!mounted) return;
    setState(() => _saved = true);
    Future.delayed(const Duration(seconds: 2), () {
      if (mounted) setState(() => _saved = false);
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    return Scaffold(
      backgroundColor: T.bg,
      appBar: AppBar(
        backgroundColor: T.bg,
        elevation: 0,
        title: const Text('Settings', style: T.title),
      ),
      body: RefreshIndicator(
        onRefresh: () => context.read<AppState>().refreshApp(),
        color: T.accent,
        backgroundColor: T.surface,
        child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 40),
        children: [
          const Text('Server', style: T.meta),
          const SizedBox(height: 8),
          TextField(
            controller: _serverUrl,
            keyboardType: TextInputType.url,
            style: const TextStyle(color: T.text),
            onSubmitted: (_) => _save(),
            decoration: InputDecoration(
              hintText: 'https://dross.example.com',
              hintStyle: const TextStyle(color: T.textFaint),
              filled: true,
              fillColor: T.inputBg,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(T.rCard),
                borderSide: const BorderSide(color: T.border),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(T.rCard),
                borderSide: const BorderSide(color: T.border),
              ),
            ),
          ),
          const SizedBox(height: 8),
          const Text(
              'Models, provider, and permissions are configured on the server.',
              style: T.metaFaint),
          const SizedBox(height: 20),
          Row(
            children: [
              PillButton(label: _saved ? 'Saved' : 'Save', icon: _saved ? Icons.check : Icons.save, onTap: _save),
              const Spacer(),
              if (!s.authed || s.token.isNotEmpty)
                GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: () => context.read<AppState>().signOut(),
                  child: const Padding(
                    padding: EdgeInsets.all(8),
                    child: Text('Sign out', style: TextStyle(color: T.danger)),
                  ),
                ),
            ],
          ),
        ],
        ),
      ),
    );
  }
}
