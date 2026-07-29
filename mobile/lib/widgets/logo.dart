import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../theme.dart';

/// The ergon mark: a Jedi ergon **cube** that bursts open — its six panels
/// separate outward from a glowing core, the way a Force user opens one — then
/// hangs open, slowly rotating. Replays on hover/tap. Pseudo-3D via CustomPaint
/// (project 8 vertices, painter's-algorithm face sort), so it stays crisp and
/// needs no 3D engine.
class ErgonLogo extends StatefulWidget {
  const ErgonLogo({super.key, this.size = 72, this.showWordmark = true});
  final double size;
  final bool showWordmark;

  @override
  State<ErgonLogo> createState() => _ErgonLogoState();
}

class _ErgonLogoState extends State<ErgonLogo>
    with TickerProviderStateMixin {
  late final AnimationController _spin = AnimationController(
      vsync: this, duration: const Duration(seconds: 12))
    ..repeat();
  late final AnimationController _open = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 2000));
  // Assembled cube at rest; bursts the panels outward with an elastic overshoot
  // while opening, and collapses smoothly back into a cube when closing.
  late final Animation<double> _burst = CurvedAnimation(
      parent: _open,
      curve: Curves.elasticOut,
      reverseCurve: Curves.easeInCubic);

  @override
  void dispose() {
    _spin.dispose();
    _open.dispose();
    super.dispose();
  }

  void _openUp(_) => _open.forward();
  void _close(_) => _open.reverse();

  void _toggle() {
    // Touch fallback (no hover): tap opens, tap again reassembles.
    if (_open.status == AnimationStatus.completed ||
        _open.status == AnimationStatus.forward) {
      _open.reverse();
    } else {
      _open.forward();
    }
  }

  @override
  Widget build(BuildContext context) {
    final cube = MouseRegion(
      onEnter: _openUp,
      onExit: _close,
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: _toggle,
        child: SizedBox(
          width: widget.size,
          height: widget.size,
          child: AnimatedBuilder(
            animation: Listenable.merge([_spin, _burst]),
            builder: (_, __) => CustomPaint(
              painter: _CubePainter(
                spin: _spin.value * 2 * math.pi,
                open: _burst.value.clamp(0.0, 1.4),
              ),
            ),
          ),
        ),
      ),
    );

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        cube,
        if (widget.showWordmark) ...[
          SizedBox(height: widget.size * 0.28),
          Text(
            'Ergon',
            style: TextStyle(
              color: T.text,
              fontSize: widget.size * 0.32,
              fontWeight: FontWeight.w600,
              letterSpacing: -widget.size * 0.005,
            ),
          ),
        ],
      ],
    );
  }
}

/// A small continuously-spinning ergon cube, for loading / running-task
/// indicators (the app's spinner in place of a CircularProgressIndicator).
class ErgonSpinner extends StatefulWidget {
  const ErgonSpinner({super.key, this.size = 28});
  final double size;
  @override
  State<ErgonSpinner> createState() => _ErgonSpinnerState();
}

class _ErgonSpinnerState extends State<ErgonSpinner>
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
            painter: _CubePainter(spin: _c.value * 2 * math.pi, open: 0),
          ),
        ),
      );
}

class _V3 {
  const _V3(this.x, this.y, this.z);
  final double x, y, z;
}

class _CubePainter extends CustomPainter {
  _CubePainter({required this.spin, required this.open});
  final double spin; // radians (continuous Y rotation)
  final double open; // 0 = assembled, ~1 = burst (elastic overshoot to ~1.4)

  static const double _h = 0.5; // half-edge
  static const double _gap = 0.85; // how far panels fly out
  static const double _tilt = 0.5; // fixed X tilt so it reads as 3D

  // Ergon palette: gold frame + translucent blue glass, per the Rebels prop.
  static const _gold = Color(0xFFC9A24B);
  static const _goldHi = Color(0xFFE6C87A);
  static const _glass = Color(0xFF4B9CD3);
  static const _glassHi = Color(0xFFAED8F0);

  // 8 corners.
  static const _corners = <_V3>[
    _V3(-_h, -_h, -_h), _V3(_h, -_h, -_h), _V3(_h, _h, -_h), _V3(-_h, _h, -_h),
    _V3(-_h, -_h, _h), _V3(_h, -_h, _h), _V3(_h, _h, _h), _V3(-_h, _h, _h),
  ];

  // 6 faces: corner indices (CCW) + outward normal.
  static const _faces = <(List<int>, _V3)>[
    ([4, 5, 6, 7], _V3(0, 0, 1)), // front
    ([1, 0, 3, 2], _V3(0, 0, -1)), // back
    ([0, 4, 7, 3], _V3(-1, 0, 0)), // left
    ([5, 1, 2, 6], _V3(1, 0, 0)), // right
    ([0, 1, 5, 4], _V3(0, -1, 0)), // top
    ([3, 7, 6, 2], _V3(0, 1, 0)), // bottom
  ];

  _V3 _rotate(_V3 p) {
    // rotate Y
    final x1 = p.x * math.cos(spin) + p.z * math.sin(spin);
    final z1 = -p.x * math.sin(spin) + p.z * math.cos(spin);
    // rotate X (fixed tilt)
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

    // Glowing core, brightening as the cube opens.
    final coreR = size.width * (0.10 + 0.16 * open.clamp(0, 1));
    canvas.drawCircle(
      c,
      coreR * 2.2,
      Paint()
        ..shader = RadialGradient(colors: [
          T.accentHi.withValues(alpha: 0.45 * open.clamp(0, 1)),
          T.accentHi.withValues(alpha: 0.0),
        ]).createShader(Rect.fromCircle(center: c, radius: coreR * 2.2)),
    );
    canvas.drawCircle(
        c, coreR, Paint()..color = T.accentHi.withValues(alpha: 0.9 * open.clamp(0, 1)));

    // Holographic light rays fanning out of the core once it's open.
    final rayT = ((open - 0.25) / 0.75).clamp(0.0, 1.0);
    if (rayT > 0) {
      const n = 10;
      final len = size.width * 0.6 * rayT;
      final rp = Paint()
        ..strokeCap = StrokeCap.round
        ..strokeWidth = size.width * 0.01
        ..color = T.accentHi.withValues(alpha: 0.14 * rayT);
      for (var i = 0; i < n; i++) {
        final a = spin * 0.4 + i * 2 * math.pi / n;
        canvas.drawLine(
            c, c + Offset(math.cos(a), math.sin(a)) * len, rp);
      }
    }

    // Build faces, offset outward by the burst, rotate, and depth-sort.
    final built = <(double depth, List<Offset> pts, _V3 nrot)>[];
    for (final (idx, n) in _faces) {
      final off = _V3(n.x * _gap * open, n.y * _gap * open, n.z * _gap * open);
      final pts = <Offset>[];
      var depth = 0.0;
      for (final i in idx) {
        final v = _corners[i];
        final moved = _V3(v.x + off.x, v.y + off.y, v.z + off.z);
        final r = _rotate(moved);
        depth += r.z;
        pts.add(_project(r, c, scale));
      }
      depth /= idx.length;
      built.add((depth, pts, _rotate(_V3(n.x, n.y, n.z))));
    }
    built.sort((a, b) => a.$1.compareTo(b.$1)); // far → near

    final w = size.width;
    for (final (_, pts, nrot) in built) {
      // Shade by how much the (rotated) normal faces the viewer (+z).
      final facing = (0.5 + 0.5 * nrot.z).clamp(0.0, 1.0);
      final face = Path()..addPolygon(pts, true);

      // Translucent blue glass.
      canvas.drawPath(
        face,
        Paint()..color = Color.lerp(_glass, _glassHi, facing)!.withValues(alpha: 0.40),
      );
      // Gold frame edge.
      canvas.drawPath(
        face,
        Paint()
          ..style = PaintingStyle.stroke
          ..strokeJoin = StrokeJoin.round
          ..strokeWidth = w * 0.024
          ..color = Color.lerp(_gold, _goldHi, facing)!,
      );

      // Geometric gold tracery: an inset square plus a diamond of edge midpoints.
      final ctr = (pts[0] + pts[1] + pts[2] + pts[3]) / 4;
      final inner = [for (final p in pts) Offset.lerp(p, ctr, 0.34)!];
      final mids = [for (var i = 0; i < 4; i++) (pts[i] + pts[(i + 1) % 4]) / 2];
      final trace = Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = w * 0.012
        ..color = _gold.withValues(alpha: 0.85 * (0.4 + 0.6 * facing));
      canvas.drawPath(Path()..addPolygon(inner, true), trace);
      canvas.drawPath(Path()..addPolygon(mids, true), trace);
    }
  }

  @override
  bool shouldRepaint(covariant _CubePainter old) =>
      old.spin != spin || old.open != open;
}
