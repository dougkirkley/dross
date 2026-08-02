import 'package:flutter/material.dart';

import '../theme.dart';

/// Swipe a card to arm deleting it.
///
/// Red washes across the card under the finger, so the gesture reads as
/// destructive while it is still being made and abandoning it costs nothing.
/// Past halfway it latches open and a delete button appears where the card's
/// own delete button used to sit — the confirmation is that second, deliberate
/// tap on a small target, rather than a sheet thrown up from the bottom of the
/// screen for something the swipe already said.
///
/// Nothing is deleted by the swipe alone. A gesture that destroys on release is
/// too easy to complete by accident while scrolling a list.
class SwipeToDelete extends StatefulWidget {
  const SwipeToDelete({
    super.key,
    required this.child,
    required this.onDelete,
  });

  final Widget child;
  final VoidCallback onDelete;

  @override
  State<SwipeToDelete> createState() => _SwipeToDeleteState();
}

class _SwipeToDeleteState extends State<SwipeToDelete> {
  /// How far the wash has been dragged, 0..1 of the card's width.
  double _t = 0;
  bool _armed = false;

  void _update(DragUpdateDetails d, double width) {
    if (width <= 0) return;
    setState(() {
      // Leftwards only: this is a destructive gesture and should not be
      // reachable by swiping the other way.
      _t = (_t - d.delta.dx / width).clamp(0.0, 1.0);
      _armed = false;
    });
  }

  void _end() {
    setState(() {
      // Past halfway the wash stays; short of it the card comes back.
      _armed = _t > 0.5;
      _t = _armed ? 1.0 : 0.0;
    });
  }

  void _reset() => setState(() {
        _t = 0;
        _armed = false;
      });

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, box) {
      final w = box.maxWidth;
      return GestureDetector(
        onHorizontalDragUpdate: (d) => _update(d, w),
        onHorizontalDragEnd: (_) => _end(),
        // No onTap here. A tap recogniser on this detector competes with the
        // delete button's own, and the card resetting instead of deleting is
        // how a button that is plainly there does nothing when pressed.
        // Dismissing is handled by the layer below the button instead.
        child: Stack(children: [
          widget.child,
          // The wash: a red panel anchored to the trailing edge, as wide as the
          // finger has travelled. Ignores pointers so the drag keeps flowing to
          // the gesture detector underneath it.
          Positioned.fill(
            child: IgnorePointer(
              child: Align(
                alignment: Alignment.centerRight,
                child: FractionallySizedBox(
                  widthFactor: _t.clamp(0.0, 1.0),
                  child: Container(
                    decoration: BoxDecoration(
                      color: T.danger.withValues(alpha: 0.85),
                      borderRadius: BorderRadius.circular(T.rCard),
                    ),
                  ),
                ),
              ),
            ),
          ),
          // Below the button and above the card: a tap anywhere else on an
          // armed card puts it back, which is the first thing anyone tries.
          if (_armed)
            Positioned.fill(
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: _reset,
              ),
            ),
          // Last, so it is topmost and wins the tap that deletes.
          if (_armed)
            Positioned(
              top: 8,
              right: 8,
              child: Material(
                color: Colors.transparent,
                child: IconButton(
                  icon: const Icon(Icons.delete_outline, color: Colors.white),
                  tooltip: 'Delete session',
                  onPressed: widget.onDelete,
                ),
              ),
            ),
        ]),
      );
    });
  }
}
