import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/models.dart';
import 'package:dross/widgets/activity.dart';

Event ev(String kind, String data) => Event(seq: 1, kind: kind, data: data);

const _bash = '{"name":"bash","input":{"command":"go build ./..."}}';

Future<void> show(WidgetTester tester, List<Event> events) async {
  final timeline = buildTimeline(events);
  await tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: ListView(
        children: [
          for (final item in timeline)
            if (item is Activity) ActivityBlock(item),
        ],
      ),
    ),
  ));
  await tester.pumpAndSettle();
}

void main() {
  // The bash tool reports a non-zero exit as ordinary content, so a failed
  // command looked exactly like a successful one — and the real error sat
  // behind two chevrons while the model's account of it was the only thing on
  // screen. A model that says "no network access" over an error that says
  // something else entirely is why this matters.
  const failed =
      'main.go:2:8: no required module provides package github.com/google/uuid\n'
      '[exit status: exit status 1]';

  testWidgets('a failed command shows its own error without being opened',
      (tester) async {
    await show(tester, [ev('tool', _bash), ev('tool-result', failed)]);
    expect(find.textContaining('no required module provides package'),
        findsOneWidget);
  });

  testWidgets('a command that worked stays folded away', (tester) async {
    await show(tester, [ev('tool', _bash), ev('tool-result', 'ok  0.1s')]);
    expect(find.textContaining('ok  0.1s'), findsNothing);
  });

  test('failure is read from the exit status the tool appends', () {
    final timeline =
        buildTimeline([ev('tool', _bash), ev('tool-result', failed)]);
    final act = timeline.whereType<Activity>().single;
    expect(act.failed, isTrue);
    expect(act.steps.single.failed, isTrue);
  });

  test('a run with no failure in it is not marked as one', () {
    final timeline =
        buildTimeline([ev('tool', _bash), ev('tool-result', 'ok  0.1s')]);
    expect(timeline.whereType<Activity>().single.failed, isFalse);
  });
}
