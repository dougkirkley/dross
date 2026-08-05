import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/widgets/row_actions.dart';
import 'package:dross/widgets/swipe_actions.dart';

void main() {
  Future<void> pump(
    WidgetTester tester,
    TargetPlatform platform,
    List<String> log,
  ) async {
    await tester.pumpWidget(MaterialApp(
      theme: ThemeData(platform: platform),
      home: Scaffold(
        body: Align(
          alignment: Alignment.topCenter,
          child: SizedBox(
            width: 400,
            child: RowActions(
              actions: [
                SwipeAction(
                  icon: Icons.drive_file_rename_outline,
                  label: 'Rename',
                  onTap: () => log.add('rename'),
                ),
                SwipeAction(
                  icon: Icons.delete_outline,
                  label: 'Delete',
                  destructive: true,
                  onTap: () => log.add('delete'),
                ),
              ],
              child: GestureDetector(
                onTap: () => log.add('row'),
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
      ),
    ));
    await tester.pumpAndSettle();
  }

  /// Puts a pointer over the row, which is what reveals the menu button.
  Future<void> hover(WidgetTester tester) async {
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    addTearDown(mouse.removePointer);
    await tester.pump();
    await mouse.moveTo(tester.getCenter(find.text('a session')));
    await tester.pumpAndSettle();
  }

  // The reason this widget exists. Nobody drags a list row sideways with a
  // mouse, so on the web these actions may as well not have been there.
  testWidgets('a pointer gets a menu, not a swipe', (tester) async {
    await pump(tester, TargetPlatform.macOS, []);
    expect(find.byType(SwipeActions), findsNothing);

    await hover(tester);
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();

    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Delete'), findsOneWidget);
  });

  // A thumb keeps the gesture: a hover-revealed control on a device that
  // cannot hover is a control that never appears.
  testWidgets('a finger still gets the swipe', (tester) async {
    await pump(tester, TargetPlatform.iOS, []);
    expect(find.byType(SwipeActions), findsOneWidget);
    expect(find.byIcon(Icons.more_vert), findsNothing);
  });

  // The button is invisible until hovered, and an invisible button must not
  // eat the clicks meant for the row underneath it — which is exactly where a
  // pointer that has not hovered yet will click.
  testWidgets('the hidden menu button does not swallow taps', (tester) async {
    final log = <String>[];
    await pump(tester, TargetPlatform.macOS, log);
    // Where the button sits when shown: hard against the right edge.
    final row = tester.getRect(find.text('a session'));
    await tester.tapAt(Offset(row.right - 16, row.center.dy));
    await tester.pumpAndSettle();

    expect(log, ['row']);
    expect(find.text('Rename'), findsNothing, reason: 'the menu opened');
  });

  // A menu item is one click from wherever the pointer was, where the swipe
  // already took a drag and then a tap on a small red target.
  testWidgets('deleting from the menu asks first', (tester) async {
    final log = <String>[];
    await pump(tester, TargetPlatform.macOS, log);
    await hover(tester);
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Delete'));
    await tester.pumpAndSettle();

    expect(log, isEmpty, reason: 'deleted without asking');
    expect(find.text('This cannot be undone.'), findsOneWidget);

    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(log, isEmpty);
  });

  // Renaming is not destructive, so it opens straight away — a confirmation on
  // everything trains people to click through the one that matters.
  testWidgets('renaming from the menu runs at once', (tester) async {
    final log = <String>[];
    await pump(tester, TargetPlatform.macOS, log);
    await hover(tester);
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Rename'));
    await tester.pumpAndSettle();

    expect(log, ['rename']);
  });
}
