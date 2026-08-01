import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/widgets/logo.dart';

/// Regenerates the PWA/launcher icons from the same vector the app draws, so
/// the installed icon can never drift from the in-app mascot. Writes to
/// /tmp/dross_icons; the build step copies them into web/icons.
///
/// Run: flutter test test/icon_gen_test.dart
void main() {
  testWidgets('generate app icons', (tester) async {
    // Plain icon: mark on the app's void ground, near full-bleed.
    await _render(tester, '/tmp/dross_icons/Icon-512.png', 512, inset: 0.86);
    // Maskable: platforms crop to a circle, so keep the mark inside the ~80%
    // safe zone and let the background bleed to the edges.
    await _render(tester, '/tmp/dross_icons/Icon-maskable-512.png', 512,
        inset: 0.60);
  });
}

Future<void> _render(WidgetTester tester, String path, double size,
    {required double inset}) async {
  final key = GlobalKey();
  // The capture is the whole test surface, so size it to the icon.
  await tester.binding.setSurfaceSize(Size(size, size));
  addTearDown(() => tester.binding.setSurfaceSize(null));
  await tester.pumpWidget(MaterialApp(
    home: RepaintBoundary(
      key: key,
      child: Container(
        width: size,
        height: size,
        // The Aetheric ground, warmed slightly so the violet mark separates.
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            center: Alignment(-0.3, -0.4),
            radius: 1.1,
            colors: [Color(0xFF1B1440), Color(0xFF07070F)],
          ),
        ),
        child: Center(
          child: SizedBox(
            width: size * inset,
            height: size * inset,
            child: CustomPaint(painter: _MarkPainter()),
          ),
        ),
      ),
    ),
  ));
  await tester.pumpAndSettle();
  await tester.runAsync(() async {
    final boundary =
        key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final image = await boundary.toImage(pixelRatio: 1);
    final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
    final f = File(path);
    f.parent.createSync(recursive: true);
    f.writeAsBytesSync(bytes!.buffer.asUint8List());
  });
}

class _MarkPainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) => paintDross(canvas, size);
  @override
  bool shouldRepaint(covariant CustomPainter old) => false;
}
