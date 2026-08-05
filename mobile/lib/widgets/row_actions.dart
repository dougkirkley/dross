import 'package:flutter/material.dart';

import '../theme.dart';
import 'swipe_actions.dart';

/// A list row and the things that can be done to it, offered the way the input
/// device asks for them.
///
/// A finger gets the swipe: the row slides aside and the buttons are in the
/// space it leaves. A pointer gets a menu on the row instead, revealed when the
/// cursor is over it.
///
/// Because swiping is a thumb idiom and nothing else. Dragging a list row
/// sideways with a mouse is not something anyone does unprompted, so on the web
/// the rename and delete this offers were, in practice, not there at all — the
/// only way to find them was to know they existed and to drag.
///
/// The actions themselves are the same objects either way, so the two
/// presentations cannot drift into offering different things.
class RowActions extends StatefulWidget {
  const RowActions({
    super.key,
    required this.child,
    required this.actions,
  });

  final Widget child;
  final List<SwipeAction> actions;

  @override
  State<RowActions> createState() => _RowActionsState();
}

class _RowActionsState extends State<RowActions> {
  bool _hovered = false;
  bool _menuOpen = false;

  @override
  Widget build(BuildContext context) {
    // Gated on the pointer, not the width. A tablet in landscape is wide and is
    // still touched, and a hover-revealed control on a device that cannot hover
    // is a control that never appears.
    //
    // Theme.platform rather than defaultTargetPlatform, matching SendOnEnter:
    // it is the documented override point, so a test can say which behaviour it
    // wants without reaching for a global.
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.android ||
      TargetPlatform.iOS ||
      TargetPlatform.fuchsia =>
        true,
      _ => false,
    };
    if (touch) {
      return SwipeActions(actions: widget.actions, child: widget.child);
    }

    // Shown while the cursor is on the row, and kept shown while the menu it
    // opened is up — the pointer has left the row by then, and a button that
    // vanishes from under its own open menu reads as a misdraw.
    final show = _hovered || _menuOpen;
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: Stack(children: [
        widget.child,
        Positioned(
          right: 6,
          top: 0,
          bottom: 0,
          child: Center(
            child: AnimatedOpacity(
              opacity: show ? 1 : 0,
              duration: const Duration(milliseconds: 120),
              // Not just invisible: an unseen button that still takes clicks
              // would eat taps meant for the row underneath it.
              child: IgnorePointer(
                ignoring: !show,
                child: _menu(context),
              ),
            ),
          ),
        ),
      ]),
    );
  }

  Widget _menu(BuildContext context) => PopupMenuButton<int>(
        icon: const Icon(Icons.more_vert, size: 18, color: T.textDim),
        tooltip: 'Actions',
        color: T.surface,
        position: PopupMenuPosition.under,
        padding: EdgeInsets.zero,
        splashRadius: 18,
        onOpened: () => setState(() => _menuOpen = true),
        onCanceled: () => setState(() => _menuOpen = false),
        onSelected: (i) {
          setState(() => _menuOpen = false);
          final a = widget.actions[i];
          if (a.destructive) {
            _confirm(context, a);
            return;
          }
          a.onTap();
        },
        itemBuilder: (_) => [
          for (final (i, a) in widget.actions.indexed)
            PopupMenuItem(
              value: i,
              height: 40,
              child: Row(children: [
                Icon(a.icon,
                    size: 18, color: a.destructive ? T.danger : T.textDim),
                const SizedBox(width: 10),
                Text(a.label,
                    style: a.destructive
                        ? T.body.copyWith(color: T.danger)
                        : T.body),
              ]),
            ),
        ],
      );

  /// Asks before doing something that cannot be undone.
  ///
  /// Only on this path. The swipe reaches the same action through a drag and
  /// then a tap on a small red target, which is already two deliberate acts; a
  /// menu item is one click from wherever the pointer happened to be.
  Future<void> _confirm(BuildContext context, SwipeAction a) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (dctx) => AlertDialog(
        backgroundColor: T.surface,
        title: Text(a.label, style: T.title),
        content: const Text('This cannot be undone.', style: T.body),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dctx, false),
            child: const Text('Cancel', style: TextStyle(color: T.textDim)),
          ),
          TextButton(
            onPressed: () => Navigator.pop(dctx, true),
            child: Text(a.label, style: const TextStyle(color: T.danger)),
          ),
        ],
      ),
    );
    if (ok == true) a.onTap();
  }
}
