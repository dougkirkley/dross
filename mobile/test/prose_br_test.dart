import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/widgets/prose.dart';

Future<void> show(WidgetTester tester, String md) =>
    tester.pumpWidget(MaterialApp(
        home: Scaffold(body: SingleChildScrollView(child: ProseText(md)))));

void main() {
  // Nothing here renders HTML, so a <br> arrived as those four characters in
  // the middle of a sentence — most visibly inside table cells, which is where
  // a model reaches for one.
  testWidgets('a <br> is not printed as text', (tester) async {
    await show(tester, 'go build -o dross<br>cd dross');
    expect(find.textContaining('<br>'), findsNothing);
    expect(find.textContaining('go build -o dross cd dross'), findsOneWidget);
  });

  testWidgets('its variants go too', (tester) async {
    await show(tester, 'one<br/>two<br />three<BR>four');
    expect(find.textContaining('<'), findsNothing);
  });
}
