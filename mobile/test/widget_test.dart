import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'package:holocron/app_state.dart';
import 'package:holocron/screens/sessions_screen.dart';

void main() {
  testWidgets('unconfigured app prompts to open settings', (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider(
        create: (_) => AppState(), // no baseUrl → not configured
        child: const MaterialApp(home: SessionsScreen()),
      ),
    );
    expect(find.text('Set your server URL to begin.'), findsOneWidget);
    expect(find.text('Open Settings'), findsOneWidget);
  });
}
