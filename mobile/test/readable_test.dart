import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:dross/theme.dart';
import 'package:dross/widgets/readable.dart';

void main() {
  const inner = SizedBox(key: Key('inner'), width: double.infinity, height: 10);

  // The default test surface is 800 wide — narrower than a desktop composer,
  // which is the case being tested.
  Future<double> widthOf(WidgetTester tester, Widget w,
      {double surface = 1800}) async {
    tester.view.physicalSize = Size(surface, 900);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: w)));
    return tester.getSize(find.byKey(const Key('inner'))).width;
  }

  // Prose keeps a reading column: long lines are hard to track back to.
  testWidgets('the transcript stays at reading width', (tester) async {
    expect(await widthOf(tester, const Readable(child: inner)), T.readWidth);
  });

  // The composer does not. Holding the input to the same column left a desktop
  // window mostly empty either side of it for no reason anyone could see.
  testWidgets('the composer is wider than the transcript', (tester) async {
    final w = await widthOf(
        tester, const Readable(maxWidth: T.composeWidth, child: inner));
    expect(w, T.composeWidth);
    expect(w, greaterThan(T.readWidth));
  });

  // On a narrow window neither cap applies — a phone fills the screen.
  testWidgets('a narrow window is unaffected', (tester) async {
    final w = await widthOf(
        tester, const Readable(maxWidth: T.composeWidth, child: inner),
        surface: 400);
    expect(w, 400);
  });
}
