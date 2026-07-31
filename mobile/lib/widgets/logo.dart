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

/// Samples a Catmull-Rom spline through [pts] (endpoints duplicated).
List<Offset> _spline(List<Offset> pts, {int per = 14}) {
  final p = [pts.first, ...pts, pts.last];
  final out = <Offset>[];
  for (var i = 0; i < p.length - 3; i++) {
    final p0 = p[i], p1 = p[i + 1], p2 = p[i + 2], p3 = p[i + 3];
    for (var s = 0; s < per; s++) {
      final t = s / per, t2 = t * t, t3 = t2 * t;
      out.add(Offset(
        0.5 * (2 * p1.dx + (-p0.dx + p2.dx) * t +
            (2 * p0.dx - 5 * p1.dx + 4 * p2.dx - p3.dx) * t2 +
            (-p0.dx + 3 * p1.dx - 3 * p2.dx + p3.dx) * t3),
        0.5 * (2 * p1.dy + (-p0.dy + p2.dy) * t +
            (2 * p0.dy - 5 * p1.dy + 4 * p2.dy - p3.dy) * t2 +
            (-p0.dy + 3 * p1.dy - 3 * p2.dy + p3.dy) * t3),
      ));
    }
  }
  out.add(pts.last);
  return out;
}

/// A closed tapered-tube outline following the centerline [pts]: each sample is
/// offset perpendicular by a half-width easing from [wStart] to [wEnd]. This is
/// what makes the tentacles read as one continuous limb instead of a chain of
/// segments with visible kinks.
Path _tentacle(List<Offset> pts, double wStart, double wEnd,
    {double bulge = 0}) {
  final c = _spline(pts);
  final n = c.length;
  final left = <Offset>[], right = <Offset>[];
  for (var i = 0; i < n; i++) {
    final t = i / (n - 1);
    var w = wStart + (wEnd - wStart) * math.pow(t, 1.35);
    // Flexing swells the upper arm into a bicep: a bump centred just past the
    // elbow, so the limb reads as muscle rather than a tapered slab.
    if (bulge > 0) {
      final d = (t - 0.62) / 0.20;
      w += bulge * wStart * 0.85 * math.exp(-d * d);
    }
    final a = c[math.max(i - 1, 0)], b = c[math.min(i + 1, n - 1)];
    final d = b - a;
    final len = d.distance == 0 ? 1.0 : d.distance;
    final nx = -d.dy / len, ny = d.dx / len;
    left.add(Offset(c[i].dx + nx * w, c[i].dy + ny * w));
    right.add(Offset(c[i].dx - nx * w, c[i].dy - ny * w));
  }
  final path = Path()..moveTo(left.first.dx, left.first.dy);
  for (final p in left.skip(1)) {
    path.lineTo(p.dx, p.dy);
  }
  for (final p in right.reversed) {
    path.lineTo(p.dx, p.dy);
  }
  return path..close();
}

/// Left tentacle centreline at rest, in unit (0..1) coordinates: leaves the
/// body, sweeps down and out, then hooks back up and inward into a curl.
const _restArm = <Offset>[
  Offset(0.285, 0.541),
  Offset(0.200, 0.593),
  Offset(0.131, 0.674),
  Offset(0.115, 0.770),
  Offset(0.177, 0.837),
  Offset(0.262, 0.822),
  Offset(0.285, 0.756),
  Offset(0.254, 0.726),
];

/// Left tentacle centreline mid-flex: a double-bicep pose. The upper arm goes
/// out to the side, the elbow bends hard, the forearm rises vertically, and the
/// tip curls in beside the head like a clenched fist.
/// The arms are drawn behind the body, so the pose has to stay outside the body
/// silhouette (its left edge sits near x 0.155) or the flex vanishes behind it.
const _flexArm = <Offset>[
  Offset(0.300, 0.560), // attachment (hidden behind the body)
  Offset(0.185, 0.628), // upper arm, out and down to the elbow
  Offset(0.095, 0.648), // elbow
  Offset(0.062, 0.618), // elbow corner — tight, so the spline breaks here
  Offset(0.056, 0.548), // forearm rising vertically
  Offset(0.058, 0.455),
  Offset(0.104, 0.388), // fist, up beside the head
  Offset(0.168, 0.396), // knuckles curled inward
];

/// Left tentacle centreline for a given [curl] (0 = resting, 1 = full flex).
List<Offset> _leftTentacle(double w, double h, double curl) => [
      for (var i = 0; i < _restArm.length; i++)
        Offset(
          (_restArm[i].dx + (_flexArm[i].dx - _restArm[i].dx) * curl) * w,
          (_restArm[i].dy + (_flexArm[i].dy - _restArm[i].dy) * curl) * h,
        )
    ];

class _DrossPainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    paintDross(canvas, size, curl: 0);
  }

  @override
  bool shouldRepaint(covariant _DrossPainter oldDelegate) => false;
}

/// Draws the Dross mark: a violet blob with a bold outline, two tapered tentacle
/// arms, one big glossy eye under a heavy lid, and a small one-sided smirk.
/// [curl] 0..1 flexes both tentacles up and inward. [close] 0..1 shuts the eye.
void paintDross(Canvas canvas, Size size,
    {double curl = 0, double close = 0}) {
  const dark = Color(0xFF140A24);
  const eyeWhite = Color(0xFFF4F1FC);
  final w = size.width, h = size.height;

  final outline = Paint()
    ..style = PaintingStyle.stroke
    ..strokeWidth = w * 0.032
    ..strokeJoin = StrokeJoin.round
    ..strokeCap = StrokeCap.round
    ..color = dark;

  // Body: a slightly tilted egg (cartoon weight, not a perfect circle).
  final bodyRect = Rect.fromLTWH(w * 0.154, h * 0.067, w * 0.669, h * 0.659);
  final bodyC = bodyRect.center;
  final body = Paint()
    ..shader = const LinearGradient(
      begin: Alignment(-0.7, -1),
      end: Alignment(0.7, 1),
      colors: [Color(0xFFB292F2), Color(0xFF8B5CF6), Color(0xFF6320C9)],
      stops: [0, 0.5, 1],
    ).createShader(bodyRect);

  // Tentacles first, so the body overlaps where they attach.
  final lPts = _leftTentacle(w, h, curl);
  final rPts = [for (final p in lPts) Offset(w - p.dx, p.dy)];
  // Flexing slims the limb (a fat tube can't show an elbow at this size) and
  // adds a bicep bump plus a clenched fist at the tip.
  final wStart = w * (0.096 - 0.038 * curl);
  final wEnd = w * (0.013 + 0.021 * curl);
  for (final pts in [lPts, rPts]) {
    final t = _tentacle(pts, wStart, wEnd, bulge: curl * 0.7);
    canvas.drawPath(t, outline);
    canvas.drawPath(t, body);
    if (curl > 0.05) {
      final fist = pts[6];
      final r = w * 0.052 * curl;
      canvas.drawCircle(fist, r, outline);
      canvas.drawCircle(fist, r, body);
    }
  }

  final bodyPath = Path()..addOval(bodyRect);
  canvas.drawPath(bodyPath, body);
  canvas.drawPath(bodyPath, outline..strokeWidth = w * 0.040);

  // Eye, centred on the body.
  final eyeC = Offset(bodyC.dx, h * 0.385);
  final eyeR = w * 0.196;
  canvas.drawCircle(eyeC, eyeR, Paint()..color = eyeWhite);

  final irisC = Offset(eyeC.dx, eyeC.dy + h * 0.015);
  canvas.drawCircle(
    irisC,
    w * 0.123,
    Paint()
      ..shader = const RadialGradient(
        center: Alignment(-0.24, -0.36),
        radius: 0.74,
        colors: [
          Color(0xFFD6E6FF),
          Color(0xFF5D83CC),
          Color(0xFF23397A),
          Color(0xFF151F4C),
        ],
        stops: [0, 0.38, 0.78, 1],
      ).createShader(Rect.fromCircle(center: irisC, radius: w * 0.123)),
  );
  canvas.drawCircle(irisC, w * 0.038, Paint()..color = const Color(0xFF0F1836));
  canvas.drawOval(
    Rect.fromCenter(
        center: Offset(irisC.dx - w * 0.050, irisC.dy - h * 0.044),
        width: w * 0.069,
        height: h * 0.089),
    Paint()..color = Colors.white.withValues(alpha: 0.9),
  );
  canvas.drawCircle(Offset(irisC.dx + w * 0.062, irisC.dy + h * 0.056),
      w * 0.017, Paint()..color = Colors.white.withValues(alpha: 0.7));

  // Heavy upper lid, clipped to the eyeball so it sits flush rather than
  // floating as a band across it. `close` drops it to a full blink.
  canvas.save();
  canvas.clipPath(Path()..addOval(Rect.fromCircle(center: eyeC, radius: eyeR)));
  final lidY = eyeC.dy - eyeR * 0.50 + (eyeR * 1.55) * close;
  final lid = Path()
    ..moveTo(eyeC.dx - eyeR * 1.2, eyeC.dy - eyeR * 2)
    ..lineTo(eyeC.dx + eyeR * 1.2, eyeC.dy - eyeR * 2)
    ..lineTo(eyeC.dx + eyeR * 1.2, lidY - eyeR * 0.16)
    ..quadraticBezierTo(eyeC.dx, lidY - eyeR * 0.52, eyeC.dx - eyeR * 1.2, lidY)
    ..close();
  canvas.drawPath(lid, body);
  canvas.drawPath(
    Path()
      ..moveTo(eyeC.dx - eyeR * 1.2, lidY)
      ..quadraticBezierTo(
          eyeC.dx, lidY - eyeR * 0.52, eyeC.dx + eyeR * 1.2, lidY - eyeR * 0.16),
    Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.036
      ..strokeCap = StrokeCap.round
      ..color = dark,
  );
  canvas.restore();

  // Bold ring around the eyeball, drawn last so it stays crisp over the lid —
  // this is what gives the mark its cartoon weight.
  canvas.drawCircle(
    eyeC,
    eyeR,
    Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.036
      ..color = dark,
  );

  // Small, tight, one-sided smirk.
  canvas.drawPath(
    Path()
      ..moveTo(w * 0.385, h * 0.637)
      ..quadraticBezierTo(w * 0.485, h * 0.667, w * 0.581, h * 0.607),
    Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = w * 0.036
      ..strokeCap = StrokeCap.round
      ..color = dark,
  );
}

/// A loading / running-task indicator: the Dross spirit as an orb. While
/// [active] (working) it floats, pulses, darts, and occasionally scratches its
/// unibrow; when inactive (idle / done answering) it settles — no floating or
/// scratching, just the occasional blink. On-brand with [DrossLogo].
class DrossSpinner extends StatefulWidget {
  const DrossSpinner({super.key, this.size = 28, this.active = true});
  final double size;
  final bool active;
  @override
  State<DrossSpinner> createState() => _DrossSpinnerState();
}

class _DrossSpinnerState extends State<DrossSpinner>
    with TickerProviderStateMixin {
  // Fast loop: bob, glow pulse, blink. Slow loop: the periodic tentacle flex.
  late final AnimationController _c = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 1600))
    ..repeat();
  late final AnimationController _flex = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 4200))
    ..repeat();
  @override
  void dispose() {
    _c.dispose();
    _flex.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SizedBox(
        width: widget.size,
        height: widget.size,
        child: AnimatedBuilder(
          animation: Listenable.merge([_c, _flex]),
          builder: (_, __) => CustomPaint(
            painter: _OrbPainter(_c.value, _flex.value, widget.active),
          ),
        ),
      );
}

/// Paints the Dross orb for a loop phase t in [0,1): a gentle bob, a pulsing
/// glow, and a quick blink once per cycle. The `flex` phase (its own slower
/// loop) curls BOTH tentacles up and inward on a beat — showing off, which is
/// rather the point. When inactive it settles: no bob, no flex, just a blink.
class _OrbPainter extends CustomPainter {
  _OrbPainter(this.t, this.flex, this.active);
  final double t;
  final double flex;
  final bool active;

  @override
  void paint(Canvas canvas, Size size) {
    final w = size.width, h = size.height;
    const tau = 2 * math.pi;

    // Gentle vertical bob so the orb floats — only while active.
    final bob = active ? math.sin(tau * t) * h * 0.035 : 0.0;

    // Soft outer glow: pulses while active, steady when idle.
    final glow = active ? 0.30 + 0.18 * (0.5 + 0.5 * math.sin(tau * t)) : 0.26;
    canvas.drawCircle(
      Offset(w * 0.5, h * 0.44 + bob),
      w * 0.46,
      Paint()
        ..color = const Color(0xFF8B5CF6).withValues(alpha: glow)
        ..maskFilter = MaskFilter.blur(BlurStyle.normal, w * 0.10),
    );

    // Blink: a short window near the end of each cycle.
    double blink = 0;
    const blinkStart = 0.82, blinkEnd = 0.94;
    if (t >= blinkStart && t <= blinkEnd) {
      final p = (t - blinkStart) / (blinkEnd - blinkStart);
      blink = math.sin(p * math.pi);
    }

    // Flex: while active, BOTH tentacles curl up and inward on a beat, hold,
    // then release. Idle Dross keeps its arms down.
    double curl = 0;
    const f0 = 0.10, f1 = 0.45;
    if (active && flex >= f0 && flex <= f1) {
      final p = (flex - f0) / (f1 - f0);
      curl = math.sin(p * math.pi); // ease up, hold, ease down
    }

    canvas.save();
    canvas.translate(0, bob);
    paintDross(canvas, size, curl: curl, close: blink * 0.95);
    canvas.restore();
  }


  @override
  bool shouldRepaint(covariant _OrbPainter old) =>
      old.t != t || old.flex != flex || old.active != active;
}
