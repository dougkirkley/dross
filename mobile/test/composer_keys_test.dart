import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/widgets/composer_keys.dart';

void main() {
  Future<void> pump(WidgetTester tester, List<String> log,
      {bool enabled = true}) async {
    final ctrl = TextEditingController();
    addTearDown(ctrl.dispose);
    await tester.pumpWidget(MaterialApp(
      // Widget tests report Android, where the gate correctly declines to bind
      // Enter — a soft keyboard's Enter is the only newline key there is.
      // These cases are about the desktop behaviour, so say so.
      theme: ThemeData(platform: TargetPlatform.macOS),
      home: Scaffold(
        body: SendOnEnter(
          enabled: enabled,
          onSend: () => log.add('send'),
          child: TextField(controller: ctrl, minLines: 1, maxLines: 6),
        ),
      ),
    ));
    await tester.tap(find.byType(TextField));
    await tester.pump();
  }

  // The whole reason this exists: a multi-line TextField never fires
  // onSubmitted, so without a shortcut a desktop user had to reach for the
  // mouse to send a message they had just finished typing.
  testWidgets('Enter sends', (tester) async {
    final log = <String>[];
    await pump(tester, log);

    await tester.enterText(find.byType(TextField), 'hello');
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();

    expect(log, ['send']);
  });

  // Unbound on purpose, so it reaches the field and inserts a newline.
  testWidgets('Shift+Enter does not send', (tester) async {
    final log = <String>[];
    await pump(tester, log);
    await tester.enterText(find.byType(TextField), 'first');

    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.pump();

    expect(log, isEmpty, reason: 'Shift+Enter must not send');
  });

  // While a turn is running the button is a Stop and Enter would mean nothing,
  // so the key goes back to being an ordinary newline rather than being
  // swallowed by an action that declines to act.
  testWidgets('disabled does not send', (tester) async {
    final log = <String>[];
    await pump(tester, log, enabled: false);
    await tester.enterText(find.byType(TextField), 'typing');

    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();

    expect(log, isEmpty);
  });
}
