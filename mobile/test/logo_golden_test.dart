import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/widgets/logo.dart';

/// Renders the mark to a PNG so the vector art can be eyeballed without
/// launching the app.
void main() {
  testWidgets('render dross mark', (tester) async {
    final key = GlobalKey();
    await tester.pumpWidget(MaterialApp(
      home: RepaintBoundary(
        key: key,
        child: const ColoredBox(
          color: Color(0xFF0E0E1A),
          child: Center(child: DrossLogo(size: 220, showWordmark: false)),
        ),
      ),
    ));
    await tester.pumpAndSettle();

    await tester.runAsync(() async {
      final boundary =
          key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 3);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      File('/tmp/dross_mark_flutter.png')
          .writeAsBytesSync(bytes!.buffer.asUint8List());
    });
  });

  testWidgets('render dross flex frame', (tester) async {
    final key = GlobalKey();
    await tester.pumpWidget(MaterialApp(
      home: RepaintBoundary(
        key: key,
        child: ColoredBox(
          color: const Color(0xFF0E0E1A),
          child: Center(
            child: SizedBox(
                width: 220,
                height: 220,
                child: CustomPaint(painter: _FlexPaint())),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    await tester.runAsync(() async {
      final boundary =
          key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 3);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      File('/tmp/dross_flex_flutter.png')
          .writeAsBytesSync(bytes!.buffer.asUint8List());
    });
  });
}

/// Renders a mid-flex spinner frame (both tentacles curled up).
class _FlexPaint extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) => paintDross(canvas, size, curl: 1);
  @override
  bool shouldRepaint(covariant CustomPainter old) => false;
}
