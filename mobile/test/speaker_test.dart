import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/theme.dart';
import 'package:dross/widgets/speaker.dart';

/// The rule is the 2px-wide coloured Container inside a SpeakerTurn.
Color ruleColor(WidgetTester tester) {
  final c = tester.widget<Container>(find.descendant(
    of: find.byType(SpeakerTurn),
    matching: find.byWidgetPredicate(
        (w) => w is Container && w.constraints?.maxWidth == 2),
  ));
  return c.color!;
}

/// Shows a turn the way a transcript does: sized to its own content, not
/// stretched to the window. A SpeakerTurn takes the height it is given, and
/// every real caller is a ListView row, which gives it the height of its text.
Future<void> show(WidgetTester tester, Widget child, {double width = 400}) =>
    tester.pumpWidget(MaterialApp(
      home: MediaQuery(
        data: MediaQueryData(size: Size(width, 800)),
        child: Scaffold(
          body: Column(mainAxisSize: MainAxisSize.min, children: [child]),
        ),
      ),
    ));

void main() {
  // The colour is the whole signal, so it has to be the one the terminal uses.
  // A session read in the TUI and in the browser at once should not change
  // which colour means whom.
  testWidgets('the rule carries the terminal colours', (tester) async {
    await show(
        tester, const SpeakerTurn(speaker: Speaker.you, child: Text('hi')));
    expect(ruleColor(tester), T.youRule);

    await show(
        tester, const SpeakerTurn(speaker: Speaker.agent, child: Text('hi')));
    expect(ruleColor(tester), T.agentRule);
  });

  // A turn is marked down its whole height, which is the reason the rule beat a
  // glyph prefix: a prefix marks one line of a twenty-line answer.
  testWidgets('the rule is as tall as the turn', (tester) async {
    await show(
      tester,
      const SpeakerTurn(
        speaker: Speaker.agent,
        child: Text('one\ntwo\nthree\nfour\nfive'),
      ),
    );
    final rule = find.descendant(
      of: find.byType(SpeakerTurn),
      matching: find.byWidgetPredicate(
          (w) => w is Container && w.constraints?.maxWidth == 2),
    );
    final text = find.text('one\ntwo\nthree\nfour\nfive');
    expect(tester.getSize(rule).height,
        closeTo(tester.getSize(text).height, 0.5));
  });

  // Nothing is written beside your own prompt: the colour already says it, and
  // "you" next to the thing you just typed is a word that tells you nothing.
  testWidgets('a label appears only when it is worth saying', (tester) async {
    await show(tester,
        const SpeakerTurn(speaker: Speaker.you, child: Text('a prompt')));
    expect(find.text('you'), findsNothing);

    await show(
      tester,
      const SpeakerTurn(
          speaker: Speaker.you, label: 'laptop', child: Text('a prompt')),
    );
    expect(find.text('laptop'), findsOneWidget);
  });

  // The layout does not change with the width. It used to: a bubble on a phone,
  // a "▸ you" prefix on a desktop, which made one session look like two
  // products when both were open.
  testWidgets('the turn looks the same at phone and desktop widths',
      (tester) async {
    for (final w in [380.0, 1400.0]) {
      await show(
          tester,
          const SpeakerTurn(
              speaker: Speaker.you, tinted: true, child: Text('a prompt')),
          width: w);
      expect(ruleColor(tester), T.youRule);
      // Left-aligned in both: the rule sits at the start of the row, never
      // pushed to the far margin.
      final turn = tester.getRect(find.byType(SpeakerTurn));
      final rule = tester.getRect(find.descendant(
        of: find.byType(SpeakerTurn),
        matching: find.byWidgetPredicate(
            (x) => x is Container && x.constraints?.maxWidth == 2),
      ));
      expect(rule.left, closeTo(turn.left, 0.5));
    }
  });
}
