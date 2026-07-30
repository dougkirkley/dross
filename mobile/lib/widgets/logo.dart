import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../theme.dart';

/// 5-row block font shared with the TUI logo, so the app and terminal render the
/// same DROSS wordmark. Each glyph is 5 rows of a 5-wide cell.
const Map<String, List<String>> _glyphs = {
  'D': ['████ ', '█   █', '█   █', '█   █', '████ '],
  'R': ['████ ', '█   █', '████ ', '█  █ ', '█   █'],
  'O': [' ███ ', '█   █', '█   █', '█   █', ' ███ '],
  'S': [' ████', '█    ', ' ███ ', '    █', '████ '],
};

/// Assembles [word] into 5 rows of block glyphs (two spaces between letters).
List<String> _blockRows(String word) {
  final rows = List.filled(5, '');
  for (final ch in word.toUpperCase().split('')) {
    final g = _glyphs[ch];
    if (g == null) continue;
    for (var r = 0; r < 5; r++) {
      rows[r] += '${g[r]}  ';
    }
  }
  return rows;
}

/// Dross's signature light-to-deep violet gradient (top to bottom).
const _purpleGradient = LinearGradient(
  begin: Alignment.topCenter,
  end: Alignment.bottomCenter,
  colors: [
    Color(0xFFD8B4FE),
    Color(0xFFC084FC),
    Color(0xFFA855F7),
    Color(0xFF8B5CF6),
    Color(0xFF7C3AED),
  ],
);

/// The Dross mark: the word DROSS in purple ASCII block-art, matching the TUI
/// wordmark. A purple vertical gradient gives it the brand sheen.
class DrossLogo extends StatelessWidget {
  const DrossLogo({super.key, this.size = 72, this.showWordmark = true});
  final double size;
  final bool showWordmark;

  @override
  Widget build(BuildContext context) {
    final rows = _blockRows('DROSS');
    // The block is ~34 cells wide; scale the monospace cell so it fills `size`.
    final cell = size / 22;
    final art = ShaderMask(
      shaderCallback: (rect) => _purpleGradient.createShader(rect),
      blendMode: BlendMode.srcIn,
      child: Text(
        rows.join('\n'),
        style: TextStyle(
          fontFamily: 'monospace',
          fontFamilyFallback: const ['Menlo', 'Courier'],
          height: 1.0,
          fontSize: cell,
          color: Colors.white,
          letterSpacing: -0.5,
          fontWeight: FontWeight.w700,
        ),
      ),
    );

    if (!showWordmark) return art;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        art,
        SizedBox(height: size * 0.22),
        Text(
          'Dross',
          style: TextStyle(
            color: T.text,
            fontSize: size * 0.30,
            fontWeight: FontWeight.w600,
            letterSpacing: -size * 0.005,
          ),
        ),
      ],
    );
  }
}

/// A small continuously-spinning cube for loading / running-task indicators
/// (the app's spinner in place of a CircularProgressIndicator), recolored to the
/// Dross purple palette.
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
  final double spin; // radians (continuous Y rotation)

  static const double _h = 0.5; // half-edge
  static const double _tilt = 0.5; // fixed X tilt so it reads as 3D

  // Purple palette: violet frame + translucent violet glass.
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
    built.sort((a, b) => a.$1.compareTo(b.$1)); // far → near

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
