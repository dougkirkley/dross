import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';

/// A single Google SSO gate shown at startup when the app is server-backed
/// (remote) but not yet signed in. Once signed in, this never shows again — the
/// same session covers the whole app (no separate per-feature login). On web the
/// same-origin Cloudflare Access session already authenticates, so this screen
/// is skipped entirely.
class SignInScreen extends StatefulWidget {
  const SignInScreen({super.key});

  @override
  State<SignInScreen> createState() => _SignInScreenState();
}

class _SignInScreenState extends State<SignInScreen> {
  bool _busy = false;
  String? _error;

  Future<void> _signIn() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    final err = await context.read<AppState>().signInRemote();
    if (!mounted) return;
    setState(() {
      _busy = false;
      _error = err;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: T.bg,
      body: SafeArea(
        // Pull-to-refresh re-checks the server and any existing session, so a
        // stale sign-in screen (server was down, token just issued elsewhere)
        // can be retried with a swipe instead of a restart.
        child: RefreshIndicator(
          onRefresh: () => context.read<AppState>().refreshApp(),
          color: T.accent,
          backgroundColor: T.surface,
          child: LayoutBuilder(
            builder: (context, box) => ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                ConstrainedBox(
                  constraints: BoxConstraints(minHeight: box.maxHeight),
                  child: Center(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(36, 0, 36, 40),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const DrossLogo(size: 140),
                const SizedBox(height: 34),
                const Text('Sign in to continue',
                    style: TextStyle(color: T.text, fontSize: 22, fontWeight: FontWeight.w600)),
                const SizedBox(height: 12),
                Text(context.read<AppState>().baseUrl,
                    style: T.metaFaint, textAlign: TextAlign.center),
                const SizedBox(height: 32),
                if (_busy)
                  const DrossSpinner(size: 30)
                else
                  PillButton(
                    label: 'Sign in with Google',
                    leading: const GoogleMark(size: 20),
                    onTap: _signIn,
                  ),
                if (_error != null) ...[
                  const SizedBox(height: 16),
                  Text(_error!, style: const TextStyle(color: T.danger), textAlign: TextAlign.center),
                ],
              ],
            ),
          ),
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
