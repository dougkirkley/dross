import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'package:ergon/app_state.dart';
import 'package:ergon/screens/server_setup_screen.dart';

void main() {
  testWidgets('first-run shows the server setup screen', (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider(
        create: (_) => AppState(),
        child: const MaterialApp(home: ServerSetupScreen()),
      ),
    );
    expect(find.text('Connect to your server'), findsOneWidget);
    expect(find.byType(TextField), findsOneWidget);
  });
}
