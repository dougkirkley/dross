import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'package:dross/app_state.dart';
import 'package:dross/widgets/repo_sheet.dart';

/// The sheet against a server it cannot reach, which is also how it behaves
/// against one with no git hosts configured: an empty workspace is still
/// offered, because that is what every session was before repositories existed.
Widget _host(void Function(RepoChoice?) onDone) {
  return ChangeNotifierProvider<AppState>(
    create: (_) => AppState(),
    child: MaterialApp(
      home: Builder(
        builder: (context) => Scaffold(
          body: Center(
            child: ElevatedButton(
              onPressed: () async => onDone(await showRepoSheet(context)),
              child: const Text('new'),
            ),
          ),
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('an empty workspace is always on offer', (tester) async {
    RepoChoice? picked;
    var answered = false;
    await tester.pumpWidget(_host((c) {
      picked = c;
      answered = true;
    }));
    await tester.tap(find.text('new'));
    await tester.pumpAndSettle();

    expect(find.text('Empty workspace'), findsOneWidget);
    await tester.tap(find.text('Empty workspace'));
    await tester.pumpAndSettle();

    expect(answered, isTrue);
    expect(picked, isNotNull);
    expect(picked!.isEmpty, isTrue, reason: 'no repository was chosen');
    expect(picked!.forge, isNull);
  });

  // Backing out of the question is not the same as answering "nothing". One
  // should make a session and the other should not.
  testWidgets('dismissing the sheet chooses nothing at all', (tester) async {
    RepoChoice? picked;
    var answered = false;
    await tester.pumpWidget(_host((c) {
      picked = c;
      answered = true;
    }));
    await tester.tap(find.text('new'));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.close));
    await tester.pumpAndSettle();

    expect(answered, isTrue);
    expect(picked, isNull, reason: 'a dismissed sheet is a cancelled create');
  });
}
