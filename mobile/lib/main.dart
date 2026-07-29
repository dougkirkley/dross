import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'app_state.dart';
import 'models.dart';
import 'theme.dart';
import 'screens/chats_screen.dart';
import 'screens/sessions_screen.dart';
import 'screens/sign_in_screen.dart';
import 'screens/welcome_screen.dart';

void main() {
  runApp(
    ChangeNotifierProvider(
      create: (_) => AppState()..load(),
      child: const ErgonApp(),
    ),
  );
}

class ErgonApp extends StatelessWidget {
  const ErgonApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Ergon',
      debugShowCheckedModeBanner: false,
      theme: T.themeData(),
      home: const Home(),
    );
  }
}

/// Routes to the direct-mode chat list or the remote-session list based on the
/// selected connection mode.
class Home extends StatelessWidget {
  const Home({super.key});

  @override
  Widget build(BuildContext context) {
    final onboarded = context.select<AppState, bool>((s) => s.onboarded);
    if (!onboarded) return const WelcomeScreen();
    final mode = context.select<AppState, ConnMode>((s) => s.mode);
    if (mode == ConnMode.remote) {
      // Server-backed: one Google SSO at the start covers the whole app. Web is
      // already authed via the same-origin Cloudflare Access session.
      final authed = context.select<AppState, bool>((s) => s.authed);
      if (!authed) return const SignInScreen();
      return const SessionsScreen();
    }
    return const ChatsScreen();
  }
}

/// Human-friendly relative time ("now", "3h", "5d").
String relTime(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inSeconds < 60) return 'now';
  if (d.inMinutes < 60) return '${d.inMinutes}m';
  if (d.inHours < 24) return '${d.inHours}h';
  if (d.inDays < 7) return '${d.inDays}d';
  return '${(d.inDays / 7).floor()}w';
}
