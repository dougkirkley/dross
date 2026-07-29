import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';

/// First-run screen: enter the `ergon serve` URL. Everything else (models,
/// provider, permissions) is configured on the server, so this is the only
/// setting the app needs. After this comes the Google sign-in gate.
class ServerSetupScreen extends StatefulWidget {
  const ServerSetupScreen({super.key});

  @override
  State<ServerSetupScreen> createState() => _ServerSetupScreenState();
}

class _ServerSetupScreenState extends State<ServerSetupScreen> {
  final _ctrl = TextEditingController();

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  void _save() {
    final url = _normalize(_ctrl.text);
    if (url.isEmpty) return;
    context.read<AppState>().setServer(url); // Home re-routes to sign-in
  }

  static String _normalize(String s) {
    s = s.trim();
    if (s.isEmpty) return '';
    if (!s.startsWith('http://') && !s.startsWith('https://')) {
      s = 'https://$s';
    }
    return s.replaceAll(RegExp(r'/+$'), '');
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: T.bg,
      body: SafeArea(
        child: Center(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(32, 0, 32, 40),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Center(child: ErgonLogo(size: 132)),
                const SizedBox(height: 32),
                const Text('Connect to your server',
                    style: TextStyle(color: T.text, fontSize: 22, fontWeight: FontWeight.w600),
                    textAlign: TextAlign.center),
                const SizedBox(height: 10),
                const Text('Enter the URL of your ergon serve instance.',
                    style: T.metaFaint, textAlign: TextAlign.center),
                const SizedBox(height: 28),
                TextField(
                  controller: _ctrl,
                  autofocus: true,
                  keyboardType: TextInputType.url,
                  style: const TextStyle(color: T.text),
                  onSubmitted: (_) => _save(),
                  decoration: InputDecoration(
                    hintText: 'https://ergon.example.com',
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
                const SizedBox(height: 20),
                Center(child: PillButton(label: 'Continue', icon: Icons.arrow_forward, onTap: _save)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
