import 'dart:convert';
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
        0.5 *
            (2 * p1.dx +
                (-p0.dx + p2.dx) * t +
                (2 * p0.dx - 5 * p1.dx + 4 * p2.dx - p3.dx) * t2 +
                (-p0.dx + 3 * p1.dx - 3 * p2.dx + p3.dx) * t3),
        0.5 *
            (2 * p1.dy +
                (-p0.dy + p2.dy) * t +
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
void paintDross(Canvas canvas, Size size, {double curl = 0, double close = 0}) {
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
      ..quadraticBezierTo(eyeC.dx, lidY - eyeR * 0.52, eyeC.dx + eyeR * 1.2,
          lidY - eyeR * 0.16),
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
      vsync: this, duration: const Duration(milliseconds: 6000))
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

    // Flex: while active, BOTH tentacles hit a double-bicep pose. Ease in, HOLD
    // the pose, then ease out — a plain sine peaks for an instant and reads as a
    // twitch. Idle Dross keeps its arms down.
    double curl = 0;
    const f0 = 0.08, f1 = 0.70;
    if (active && flex >= f0 && flex <= f1) {
      final p = (flex - f0) / (f1 - f0); // 0..1 across the flex window
      const rise = 0.20, fall = 0.80; // hold occupies the middle 60%
      double e;
      if (p < rise) {
        e = p / rise;
      } else if (p > fall) {
        e = (1 - p) / (1 - fall);
      } else {
        e = 1; // holding the pose
      }
      curl = e * e * (3 - 2 * e); // smoothstep the ramps
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

/// The Google "G". Embedded as bytes rather than fetched or shipped as an asset
/// so the sign-in button carries the real mark with no network dependency.
const _googleMarkPng =
    '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';

final _googleMarkBytes = base64Decode(_googleMarkPng);

/// The four-colour Google mark, for the "Sign in with Google" button.
class GoogleMark extends StatelessWidget {
  const GoogleMark({super.key, this.size = 20});
  final double size;

  @override
  Widget build(BuildContext context) =>
      Image.memory(_googleMarkBytes, width: size, height: size);
}
