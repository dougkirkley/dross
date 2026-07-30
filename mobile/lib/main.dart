import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'app_state.dart';
import 'theme.dart';
import 'screens/chats_screen.dart';
import 'screens/server_setup_screen.dart';
import 'screens/sessions_screen.dart';
import 'screens/sign_in_screen.dart';

void main() {
  runApp(
    ChangeNotifierProvider(
      create: (_) => AppState()..load(),
      child: const DrossApp(),
    ),
  );
}

class DrossApp extends StatelessWidget {
  const DrossApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Dross',
      debugShowCheckedModeBanner: false,
      theme: T.themeData(),
      home: const Home(),
    );
  }
}

/// The app is a thin client for `dross serve`. Startup gate: pick a server URL,
/// sign in via Google SSO, then the session list. Both steps are one-time (the
/// URL is editable later in Settings; web is pre-authed via Cloudflare Access).
class Home extends StatelessWidget {
  const Home({super.key});

  @override
  Widget build(BuildContext context) {
    final configured = context.select<AppState, bool>((s) => s.remoteConfigured);
    if (!configured) return const ServerSetupScreen();
    final authed = context.select<AppState, bool>((s) => s.authed);
    if (!authed) return const SignInScreen();
    final mode = context.select<AppState, AppMode>((s) => s.mode);
    return mode == AppMode.chat ? const ChatsScreen() : const SessionsScreen();
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
