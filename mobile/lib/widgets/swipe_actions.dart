import 'package:flutter/material.dart';

import '../theme.dart';

/// One thing a swiped-open card offers to do.
class SwipeAction {
  const SwipeAction({
    required this.icon,
    required this.label,
    required this.onTap,
    this.color = T.surfaceAlt,
    this.iconColor = T.text,
  });

  final IconData icon;

  /// Read by screen readers and shown as a tooltip. The buttons are icons, so
  /// this is the only name they have.
  final String label;
  final VoidCallback onTap;
  final Color color;
  final Color iconColor;
}

/// Swipe a card aside to reveal what can be done to it.
///
/// The card itself slides under the finger and the actions sit in the space it
/// leaves, which is how a phone has said "there is more here" for fifteen years.
/// Past halfway it latches open; short of that the card comes back.
///
/// Nothing happens on the swipe alone, deliberately. A gesture that acts on
/// release is far too easy to complete by accident while scrolling a list, and
/// one of these actions deletes a session. The swipe reveals; the tap acts —
/// which is also what makes a second action possible at all, where a
/// dismiss-on-release could only ever mean one thing.
class SwipeActions extends StatefulWidget {
  const SwipeActions({
    super.key,
    required this.child,
    required this.actions,
  });

  final Widget child;

  /// Shown right to left in the order given, so the most-used action sits
  /// nearest the thumb's travel and the destructive one furthest from it.
  final List<SwipeAction> actions;

  @override
  State<SwipeActions> createState() => _SwipeActionsState();
}

class _SwipeActionsState extends State<SwipeActions>
    with SingleTickerProviderStateMixin {
  static const _button = 46.0;
  static const _gap = 10.0;

  late final AnimationController _anim = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 180),
  )..addListener(() => setState(() {}));

  /// Where the snap started and where it is going. While a finger is down these
  /// are equal and the offset is simply _to, so dragging and settling share one
  /// source of truth instead of fighting over a stored position.
  double _from = 0;
  double _to = 0;

  double get _offset =>
      _anim.isAnimating ? _from + (_to - _from) * _curve : _to;

  double get _curve => Curves.easeOut.transform(_anim.value);

  double get _open => widget.actions.length * (_button + _gap) + _gap;

  @override
  void dispose() {
    _anim.dispose();
    super.dispose();
  }

  void _drag(DragUpdateDetails d) {
    final at = _offset;
    _anim.stop();
    setState(() {
      // Leftwards only. Swiping the other way on a list row means "go back" on
      // iOS, and stealing that gesture here would break the one people use.
      _from = _to = (at - d.delta.dx).clamp(0.0, _open);
    });
  }

  void _settle() => _snapTo(_offset > _open / 2 ? _open : 0);

  void _snapTo(double target) {
    final at = _offset;
    if (at == target) return;
    _anim.stop();
    setState(() {
      _from = at;
      _to = target;
    });
    _anim.forward(from: 0);
  }

  void close() => _snapTo(0);

  @override
  Widget build(BuildContext context) {
    final open = _offset > 0;
    return GestureDetector(
      onHorizontalDragUpdate: _drag,
      onHorizontalDragEnd: (_) => _settle(),
      child: Stack(children: [
        // The actions sit underneath, revealed rather than slid in. Laid out at
        // full width so their position does not depend on how far the card has
        // travelled — only whether it has travelled far enough to show them.
        Positioned.fill(
          child: Align(
            alignment: Alignment.centerRight,
            child: Padding(
              padding: const EdgeInsets.only(right: _gap),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (final a in widget.actions) ...[
                    _actionButton(a),
                    const SizedBox(width: _gap),
                  ],
                ],
              ),
            ),
          ),
        ),
        Transform.translate(
          offset: Offset(-_offset, 0),
          child: widget.child,
        ),
        // Above the card, below the buttons: a tap anywhere on an open card
        // puts it back, which is the first thing anyone tries. Sized to the
        // card so it never covers the buttons themselves.
        if (open)
          Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            right: _offset,
            child: GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: close,
            ),
          ),
      ]),
    );
  }

  Widget _actionButton(SwipeAction a) => SizedBox(
        width: _button,
        height: _button,
        child: Material(
          color: a.color,
          shape: const CircleBorder(),
          clipBehavior: Clip.antiAlias,
          child: IconButton(
            icon: Icon(a.icon, size: 20, color: a.iconColor),
            tooltip: a.label,
            // Close first: the row this belongs to may be about to be rebuilt
            // without it (delete) or with a new name (rename), and either way
            // leaving it latched open behind a dialog is a card that looks
            // stuck when the dialog goes away.
            onPressed: () {
              close();
              a.onTap();
            },
          ),
        ),
      );
}
