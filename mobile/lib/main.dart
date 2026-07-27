import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'app_state.dart';
import 'screens/sessions_screen.dart';

// Carolina blue on near-black — matches the TUI theme.
const carolina = Color(0xFF4B9CD3);
const carolinaHi = Color(0xFF8FC7E8);
const base = Color(0xFF0C0F12);
const panel = Color(0xFF15181C);

void main() {
  runApp(
    ChangeNotifierProvider(
      create: (_) => AppState()..load(),
      child: const HolocronApp(),
    ),
  );
}

class HolocronApp extends StatelessWidget {
  const HolocronApp({super.key});

  @override
  Widget build(BuildContext context) {
    final scheme = ColorScheme.fromSeed(
      seedColor: carolina,
      brightness: Brightness.dark,
    ).copyWith(
      surface: base,
      primary: carolina,
      secondary: carolinaHi,
    );
    return MaterialApp(
      title: 'Holocron',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        useMaterial3: true,
        colorScheme: scheme,
        scaffoldBackgroundColor: base,
        appBarTheme: const AppBarTheme(backgroundColor: panel),
        fontFamily: 'monospace',
      ),
      home: const SessionsScreen(),
    );
  }
}
