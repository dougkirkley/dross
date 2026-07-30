import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../theme.dart';

/// The Dross mark: the one-eyed purple spirit from Cradle, drawn as a vector
/// (CustomPaint) so it stays crisp at any size — a round violet blob with a big
/// eye, a heavy brow, a small mouth, and two wavy tendril legs. An ASCII version
/// lives in the TUI; on mobile a painter reads far better than monospace art.
class DrossLogo extends StatelessWidget {
  const DrossLogo({super.key, this.size = 72, this.showWordmark = true});
  final double size;
  final bool showWordmark;

  @override
  Widget build(BuildContext context) {
    final mark = SizedBox(
      width: size,
      height: size,
      child: CustomPaint(painter: _DrossPainter()),
    );
    if (!showWordmark) return mark;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        mark,
        SizedBox(height: size * 0.18),
        Text(
          'Dross',
          style: TextStyle(
            color: T.text,
            fontSize: size * 0.34,
            fontWeight: FontWeight.w700,
            letterSpacing: -size * 0.005,
          ),
        ),
      ],
    );
  }
}

class _DrossPainter extends CustomPainter {
  static const _dark = Color(0xFF0C0A1F); // brow / eye / mouth
  static const _eyeWhite = Color(0xFFF3EEFF);

  @override
  void paint(Canvas canvas, Size size) {
    final w = size.width, h = size.height;
    final bodyC = Offset(w * 0.5, h * 0.38);
    final bodyR = w * 0.28;

    // Two wavy tendril legs behind the body (longer + thinner than the body).
    final legPaint = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.085
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..shader = const LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: [Color(0xFF8B5CF6), Color(0xFF7C3AED)],
      ).createShader(Rect.fromLTWH(0, h * 0.55, w, h * 0.45));
    final left = Path()
      ..moveTo(w * 0.42, h * 0.58)
      ..quadraticBezierTo(w * 0.28, h * 0.72, w * 0.38, h * 0.82)
      ..quadraticBezierTo(w * 0.48, h * 0.92, w * 0.38, h * 0.99);
    final right = Path()
      ..moveTo(w * 0.58, h * 0.58)
      ..quadraticBezierTo(w * 0.72, h * 0.72, w * 0.62, h * 0.82)
      ..quadraticBezierTo(w * 0.52, h * 0.92, w * 0.62, h * 0.99);
    canvas.drawPath(left, legPaint);
    canvas.drawPath(right, legPaint);

    // Body: violet blob with a top-lit gradient.
    final body = Paint()
      ..shader = const LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: [Color(0xFFC4A5FF), Color(0xFF7C3AED)],
      ).createShader(Rect.fromCircle(center: bodyC, radius: bodyR));
    canvas.drawCircle(bodyC, bodyR, body);

    // Big single eye, right of centre, iris looking down.
    final eyeC = Offset(w * 0.55, h * 0.38);
    canvas.drawCircle(eyeC, w * 0.13, Paint()..color = _eyeWhite);
    canvas.drawCircle(
        Offset(eyeC.dx + w * 0.02, eyeC.dy + w * 0.035), w * 0.085,
        Paint()..color = _dark);

    // Heavy brow drawn ON TOP of the eye as a lid — covers the upper third so it
    // reads half-lidded and unimpressed, the way Dross looks at you.
    final brow = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.075
      ..strokeCap = StrokeCap.round
      ..color = _dark;
    canvas.drawLine(
        Offset(w * 0.40, h * 0.31), Offset(w * 0.66, h * 0.275), brow);

    // Small mouth: a gentle smile low and centre-left of the eye.
    final mouth = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.042
      ..strokeCap = StrokeCap.round
      ..color = _dark;
    final m = Path()
      ..moveTo(w * 0.45, h * 0.56)
      ..quadraticBezierTo(w * 0.52, h * 0.605, w * 0.61, h * 0.56);
    canvas.drawPath(m, mouth);
  }

  @override
  bool shouldRepaint(covariant _DrossPainter oldDelegate) => false;
}

/// A small continuously-spinning cube for loading / running-task indicators,
/// in the Dross purple palette.
class DrossSpinner extends StatefulWidget {
  const DrossSpinner({super.key, this.size = 28});
  final double size;
  @override
  State<DrossSpinner> createState() => _DrossSpinnerState();
}

class _DrossSpinnerState extends State<DrossSpinner>
    with SingleTickerProviderStateMixin {
  late final AnimationController _c =
      AnimationController(vsync: this, duration: const Duration(seconds: 3))
        ..repeat();
  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SizedBox(
        width: widget.size,
        height: widget.size,
        child: AnimatedBuilder(
          animation: _c,
          builder: (_, __) => CustomPaint(
            painter: _CubePainter(spin: _c.value * 2 * math.pi),
          ),
        ),
      );
}

class _V3 {
  const _V3(this.x, this.y, this.z);
  final double x, y, z;
}

class _CubePainter extends CustomPainter {
  _CubePainter({required this.spin});
  final double spin;

  static const double _h = 0.5;
  static const double _tilt = 0.5;

  static const _frame = Color(0xFF8B5CF6);
  static const _frameHi = Color(0xFFC084FC);
  static const _glass = Color(0xFF7C3AED);
  static const _glassHi = Color(0xFFD8B4FE);

  static const _corners = <_V3>[
    _V3(-_h, -_h, -_h), _V3(_h, -_h, -_h), _V3(_h, _h, -_h), _V3(-_h, _h, -_h),
    _V3(-_h, -_h, _h), _V3(_h, -_h, _h), _V3(_h, _h, _h), _V3(-_h, _h, _h),
  ];

  static const _faces = <(List<int>, _V3)>[
    ([4, 5, 6, 7], _V3(0, 0, 1)),
    ([1, 0, 3, 2], _V3(0, 0, -1)),
    ([0, 4, 7, 3], _V3(-1, 0, 0)),
    ([5, 1, 2, 6], _V3(1, 0, 0)),
    ([0, 1, 5, 4], _V3(0, -1, 0)),
    ([3, 7, 6, 2], _V3(0, 1, 0)),
  ];

  _V3 _rotate(_V3 p) {
    final x1 = p.x * math.cos(spin) + p.z * math.sin(spin);
    final z1 = -p.x * math.sin(spin) + p.z * math.cos(spin);
    final y2 = p.y * math.cos(_tilt) - z1 * math.sin(_tilt);
    final z2 = p.y * math.sin(_tilt) + z1 * math.cos(_tilt);
    return _V3(x1, y2, z2);
  }

  Offset _project(_V3 p, Offset c, double scale) {
    const focal = 3.2;
    final s = focal / (focal - p.z);
    return Offset(c.dx + p.x * s * scale, c.dy + p.y * s * scale);
  }

  @override
  void paint(Canvas canvas, Size size) {
    final c = Offset(size.width / 2, size.height / 2);
    final scale = size.width * 0.42;

    final built = <(double depth, List<Offset> pts, _V3 nrot)>[];
    for (final (idx, n) in _faces) {
      final pts = <Offset>[];
      var depth = 0.0;
      for (final i in idx) {
        final r = _rotate(_corners[i]);
        depth += r.z;
        pts.add(_project(r, c, scale));
      }
      depth /= idx.length;
      built.add((depth, pts, _rotate(_V3(n.x, n.y, n.z))));
    }
    built.sort((a, b) => a.$1.compareTo(b.$1));

    final w = size.width;
    for (final (_, pts, nrot) in built) {
      final facing = (0.5 + 0.5 * nrot.z).clamp(0.0, 1.0);
      final face = Path()..addPolygon(pts, true);
      canvas.drawPath(
        face,
        Paint()
          ..color =
              Color.lerp(_glass, _glassHi, facing)!.withValues(alpha: 0.40),
      );
      canvas.drawPath(
        face,
        Paint()
          ..style = PaintingStyle.stroke
          ..strokeJoin = StrokeJoin.round
          ..strokeWidth = w * 0.024
          ..color = Color.lerp(_frame, _frameHi, facing)!,
      );
    }
  }

  @override
  bool shouldRepaint(covariant _CubePainter old) => old.spin != spin;
}
