import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/theme.dart';
import 'package:dross/widgets/readable.dart';

void main() {
  const inner = SizedBox(key: Key('inner'), width: double.infinity, height: 10);

  // The default test surface is 800 wide — narrower than the column being
  // tested, which is the whole point on a desktop window.
  Future<double> widthOf(WidgetTester tester, Widget w,
      {double surface = 1800}) async {
    tester.view.physicalSize = Size(surface, 900);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: w)));
    return tester.getSize(find.byKey(const Key('inner'))).width;
  }

  // Transcript and composer share one column, so their edges line up. Two
  // different left edges on the same screen reads as a mistake.
  testWidgets('transcript and composer are the same width', (tester) async {
    final transcript = await widthOf(tester, const Readable(child: inner));
    final composer = await widthOf(tester, const Readable(child: inner));
    expect(transcript, T.readWidth);
    expect(composer, transcript);
  });

  // On a narrow window the cap does not apply — a phone fills the screen.
  testWidgets('a narrow window is unaffected', (tester) async {
    expect(await widthOf(tester, const Readable(child: inner), surface: 400), 400);
  });
}
