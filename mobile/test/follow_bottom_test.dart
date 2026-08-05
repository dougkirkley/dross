import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/widgets/follow_bottom.dart';

void main() {
  _movingEnd();
  // A transcript that grows, the way one does while an answer is being written.
  Future<void> pump(WidgetTester tester, FollowBottom follow, int lines) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Builder(builder: (_) {
          follow.follow();
          return ListView.builder(
            controller: follow.controller,
            itemCount: lines,
            itemBuilder: (_, i) => SizedBox(height: 100, child: Text('$i')),
          );
        }),
      ),
    ));
    await tester.pumpAndSettle();
  }

  testWidgets('a growing transcript keeps the newest line in view',
      (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);

    await pump(tester, follow, 10);
    await pump(tester, follow, 30);

    final p = follow.controller.position;
    expect(p.pixels, p.maxScrollExtent);
    expect(follow.following, isTrue);
  });

  testWidgets('scrolling up stops it dragging you back down', (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);

    await pump(tester, follow, 30);
    // Read something further up.
    await tester.drag(find.byType(ListView), const Offset(0, 600));
    await tester.pumpAndSettle();
    expect(follow.following, isFalse);
    final where = follow.controller.position.pixels;

    // More of the answer arrives. It must not move the page under the reader.
    await pump(tester, follow, 40);
    expect(follow.controller.position.pixels, where);
    expect(follow.following, isFalse);
  });

  testWidgets('scrolling back to the end resumes following', (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);

    await pump(tester, follow, 30);
    await tester.drag(find.byType(ListView), const Offset(0, 600));
    await tester.pumpAndSettle();
    expect(follow.following, isFalse);

    await tester.drag(find.byType(ListView), const Offset(0, -600));
    await tester.pumpAndSettle();
    expect(follow.following, isTrue);

    await pump(tester, follow, 40);
    final p = follow.controller.position;
    expect(p.pixels, p.maxScrollExtent);
  });

  testWidgets('pin brings it back and follows again', (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);

    await pump(tester, follow, 30);
    await tester.drag(find.byType(ListView), const Offset(0, 600));
    await tester.pumpAndSettle();
    expect(follow.following, isFalse);

    follow.pin();
    await tester.pumpAndSettle();
    final p = follow.controller.position;
    expect(follow.following, isTrue);
    expect(p.pixels, p.maxScrollExtent);
  });

  testWidgets('the way back is offered only once you have left',
      (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Column(children: [
          Expanded(
            child: Builder(builder: (_) {
              follow.follow();
              return ListView.builder(
                controller: follow.controller,
                itemCount: 30,
                itemBuilder: (_, i) => SizedBox(height: 100, child: Text('$i')),
              );
            }),
          ),
          AnimatedBuilder(
            animation: follow,
            builder: (_, __) => JumpToLatest(follow: follow),
          ),
        ]),
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Latest'), findsNothing);

    await tester.drag(find.byType(ListView), const Offset(0, 600));
    await tester.pumpAndSettle();
    expect(find.text('Latest'), findsOneWidget);

    await tester.tap(find.text('Latest'));
    await tester.pumpAndSettle();
    expect(find.text('Latest'), findsNothing);
    final p = follow.controller.position;
    expect(p.pixels, p.maxScrollExtent);
  });
}

// The rows of a real transcript are all different heights — a line, a
// paragraph, a code block — so a lazily built list only knows how tall it is
// once it has laid them out. The first jump lands on an estimate, the end moves
// further down as more rows are built, and the reader is left short of it with
// the button offering to try again.
void _movingEnd() {
  Future<void> pumpRagged(WidgetTester tester, FollowBottom follow) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Builder(builder: (_) {
          follow.follow();
          return ListView.builder(
            controller: follow.controller,
            itemCount: 300,
            // Nothing here is a round number, and no two neighbours match:
            // an estimate made from the rows on screen cannot be right.
            itemBuilder: (_, i) =>
                SizedBox(height: 40.0 + (i % 11) * 37.0, child: Text('$i')),
          );
        }),
      ),
    ));
    await tester.pumpAndSettle();
  }

  testWidgets('Latest reaches the end of a list of ragged rows',
      (tester) async {
    final follow = FollowBottom();
    addTearDown(follow.dispose);
    await pumpRagged(tester, follow);

    // Somewhere near the top, the way you get when re-reading.
    follow.controller.jumpTo(0);
    await tester.pumpAndSettle();
    expect(follow.following, isFalse, reason: 'setting up the case');

    follow.pin();
    await tester.pumpAndSettle();

    final p = follow.controller.position;
    expect(p.pixels, closeTo(p.maxScrollExtent, 1),
        reason: 'stopped short of the end it was asked to go to');
    expect(follow.following, isTrue);
  });
}
