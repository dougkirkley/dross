import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/widgets/swipe_actions.dart';

void main() {
  Future<List<String>> pump(WidgetTester tester, List<String> log) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Align(
          alignment: Alignment.topCenter,
          // A fixed width, so "how far did the card travel" is a number the
          // test can reason about rather than whatever the text measured.
          child: SizedBox(
            width: 400,
            child: SwipeActions(
              actions: [
                SwipeAction(
                  icon: Icons.drive_file_rename_outline,
                  label: 'Rename',
                  onTap: () => log.add('rename'),
                ),
                SwipeAction(
                  icon: Icons.delete_outline,
                  label: 'Delete',
                  onTap: () => log.add('delete'),
                ),
              ],
              child: Container(
                height: 80,
                width: double.infinity,
                color: const Color(0xFF101010),
                child: const Text('a session'),
              ),
            ),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    return log;
  }

  // The whole point of revealing rather than dismissing: a swipe that acts on
  // release can only ever mean one thing, and deleting is not something to do
  // by accident while scrolling.
  testWidgets('a swipe reveals the actions and does nothing else',
      (tester) async {
    final log = <String>[];
    await pump(tester, log);

    await tester.drag(find.text('a session'), const Offset(-140, 0));
    await tester.pumpAndSettle();

    expect(log, isEmpty, reason: 'the swipe alone must not act');
    expect(find.byTooltip('Rename'), findsOneWidget);
    expect(find.byTooltip('Delete'), findsOneWidget);
  });

  // Short of halfway the card comes back, so brushing a row while scrolling
  // does not leave a delete button sitting under your thumb.
  testWidgets('a short swipe springs back', (tester) async {
    final log = <String>[];
    await pump(tester, log);
    final rest = tester.getTopLeft(find.text('a session')).dx;

    await tester.drag(find.text('a session'), const Offset(-20, 0));
    await tester.pumpAndSettle();

    expect(tester.getTopLeft(find.text('a session')).dx, rest);
    expect(log, isEmpty);
  });

  testWidgets('the revealed buttons are what act', (tester) async {
    final log = <String>[];
    await pump(tester, log);

    await tester.drag(find.text('a session'), const Offset(-140, 0));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Rename'));
    await tester.pumpAndSettle();
    expect(log, ['rename']);

    await tester.drag(find.text('a session'), const Offset(-140, 0));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Delete'));
    await tester.pumpAndSettle();
    expect(log, ['rename', 'delete']);
  });

  testWidgets('tapping the open card puts it back', (tester) async {
    final log = <String>[];
    await pump(tester, log);
    final rest = tester.getTopLeft(find.text('a session')).dx;

    await tester.drag(find.text('a session'), const Offset(-140, 0));
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.text('a session')).dx, lessThan(rest));

    // The first thing anyone tries with an open row: tap the card itself.
    // The 400-wide box is centred in an 800-wide screen, so this lands on the
    // card and clear of the buttons.
    await tester.tapAt(const Offset(250, 40));
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.text('a session')).dx, rest);
    expect(log, isEmpty);
  });

  // Rightwards is "go back" on iOS. Taking it here would break the gesture
  // people actually use.
  testWidgets('swiping the other way does nothing', (tester) async {
    final log = <String>[];
    await pump(tester, log);
    final rest = tester.getTopLeft(find.text('a session')).dx;

    await tester.drag(find.text('a session'), const Offset(140, 0));
    await tester.pumpAndSettle();

    expect(tester.getTopLeft(find.text('a session')).dx, rest);
    expect(log, isEmpty);
  });
}
