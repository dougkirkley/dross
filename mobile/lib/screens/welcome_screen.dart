import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/logo.dart';
import 'settings_screen.dart';

/// First-run welcome: the ergon mark, a tagline, and two ways in — start
/// chatting directly, or connect to an `ergon serve` instance.
class WelcomeScreen extends StatelessWidget {
  const WelcomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.read<AppState>();
    return Scaffold(
      backgroundColor: T.bg,
      body: SafeArea(
        child: Center(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(36, 0, 36, 40),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const ErgonLogo(size: 150),
                const SizedBox(height: 34),
                Text('Cost-aware coding,\nin your pocket.',
                    style:
                        T.prose.copyWith(fontSize: 24, height: 1.35, color: T.text),
                    textAlign: TextAlign.center),
                const SizedBox(height: 14),
                const Text(
                    'A frontier model plans; a cheaper model does the bounded '
                    'work. Drive it from your phone.',
                    style: T.metaFaint,
                    textAlign: TextAlign.center),
                const SizedBox(height: 40),
                PillButton(
                  label: 'Start chatting',
                  icon: Icons.arrow_forward,
                  onTap: () => s.completeOnboarding(),
                ),
                const SizedBox(height: 18),
                GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: () {
                    s.setMode(ConnMode.remote);
                    s.completeOnboarding();
                    Navigator.push(
                        context,
                        MaterialPageRoute(
                            builder: (_) => const SettingsScreen()));
                  },
                  child: const Padding(
                    padding: EdgeInsets.all(8),
                    child: Text('Connect a server', style: T.meta),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
