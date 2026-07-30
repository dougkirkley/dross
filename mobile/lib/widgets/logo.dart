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

/// A loading / running-task indicator: the Dross spirit as a floating orb whose
/// single eye pulses, darts side to side, and blinks, wrapped in a soft violet
/// glow. On-brand with [DrossLogo].
class DrossSpinner extends StatefulWidget {
  const DrossSpinner({super.key, this.size = 28});
  final double size;
  @override
  State<DrossSpinner> createState() => _DrossSpinnerState();
}

class _DrossSpinnerState extends State<DrossSpinner>
    with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 1600))
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
            painter: _OrbPainter(_c.value),
          ),
        ),
      );
}

/// Paints the Dross orb for a loop phase t in [0,1): a bob, a pulsing glow, an
/// eye that darts left/right, and a quick blink once per cycle.
class _OrbPainter extends CustomPainter {
  _OrbPainter(this.t);
  final double t;

  static const _dark = Color(0xFF0C0A1F);
  static const _eyeWhite = Color(0xFFF3EEFF);

  @override
  void paint(Canvas canvas, Size size) {
    final w = size.width, h = size.height;
    const tau = 2 * math.pi;

    // Gentle vertical bob so the orb floats.
    final bob = math.sin(tau * t) * h * 0.05;
    final bodyC = Offset(w * 0.5, h * 0.5 + bob);
    final bodyR = w * 0.34;

    // Soft outer glow that pulses with the cycle.
    final glow = 0.30 + 0.18 * (0.5 + 0.5 * math.sin(tau * t));
    canvas.drawCircle(
      bodyC,
      bodyR * 1.5,
      Paint()
        ..color = const Color(0xFF8B5CF6).withValues(alpha: glow)
        ..maskFilter = MaskFilter.blur(BlurStyle.normal, w * 0.10),
    );

    // Body: violet blob, top-lit.
    canvas.drawCircle(
      bodyC,
      bodyR,
      Paint()
        ..shader = const LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [Color(0xFFC4A5FF), Color(0xFF7C3AED)],
        ).createShader(Rect.fromCircle(center: bodyC, radius: bodyR)),
    );

    // Blink: a short window near the end of each cycle where the lid closes.
    // blink in [0,1]: 0 open, 1 fully shut.
    double blink = 0;
    const blinkStart = 0.82, blinkEnd = 0.94;
    if (t >= blinkStart && t <= blinkEnd) {
      final p = (t - blinkStart) / (blinkEnd - blinkStart); // 0..1
      blink = math.sin(p * math.pi); // 0 -> 1 -> 0
    }

    final eyeC = Offset(w * 0.5, bodyC.dy);
    final eyeR = w * 0.16;
    final eyeH = eyeR * (1 - 0.9 * blink); // squash vertically on blink

    // Eyeball (an oval so it can squash shut).
    canvas.drawOval(
      Rect.fromCenter(center: eyeC, width: eyeR * 2, height: eyeH * 2),
      Paint()..color = _eyeWhite,
    );

    // Pupil darts left/right; hidden while blinking.
    if (blink < 0.6) {
      final dart = math.sin(tau * t) * eyeR * 0.45;
      canvas.drawCircle(
        Offset(eyeC.dx + dart, eyeC.dy),
        eyeR * 0.55 * (1 - blink),
        Paint()..color = _dark,
      );
    }

    // Heavy half-lid brow for the unimpressed Dross look.
    canvas.drawLine(
      Offset(eyeC.dx - eyeR * 1.05, eyeC.dy - eyeR * 0.7),
      Offset(eyeC.dx + eyeR * 1.15, eyeC.dy - eyeR * 0.95),
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = w * 0.075
        ..strokeCap = StrokeCap.round
        ..color = _dark,
    );
  }

  @override
  bool shouldRepaint(covariant _OrbPainter old) => old.t != t;
}
