import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Enter sends, Shift+Enter puts in a newline — the convention every desktop
/// chat client shares, and the one a keyboard user reaches for without looking.
///
/// Wrapped around a multi-line composer, which is why this exists at all: a
/// TextField with maxLines above one treats Enter as a newline and never fires
/// onSubmitted, so a desktop user had to move to the mouse to send a message
/// they had just finished typing.
///
/// Gated on a real keyboard rather than on window width. A tablet in landscape
/// is wide and is still typed at with a soft keyboard, where Enter is the only
/// newline key there is — taking it would leave no way to write a second
/// paragraph. Physical keyboards have Shift, so they lose nothing.
class SendOnEnter extends StatelessWidget {
  const SendOnEnter({
    super.key,
    required this.child,
    required this.onSend,
    this.enabled = true,
  });

  final Widget child;
  final VoidCallback onSend;

  /// False while a send would be wrong (a run in flight, an empty composer);
  /// the key press then falls through to the field as an ordinary newline.
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    // A soft keyboard reports no shortcut modifiers and its Enter must stay a
    // newline. Desktop and web always have a physical keyboard; on a phone or
    // tablet this is only true when one is attached.
    // Theme.platform rather than defaultTargetPlatform: it is the documented
    // override point, so a caller (or a test) can say which behaviour it wants
    // without reaching for a global debug variable.
    final physical = switch (Theme.of(context).platform) {
      TargetPlatform.android || TargetPlatform.iOS || TargetPlatform.fuchsia =>
        false,
      _ => true,
    };
    // Nothing bound when a send would be wrong: the key then reaches the
    // field and behaves as it always did, rather than being swallowed by an
    // action that declines to act.
    if (!physical || !enabled) return child;

    return Shortcuts(
      shortcuts: const <ShortcutActivator, Intent>{
        // Bare Enter only. Shift+Enter is deliberately unbound so it reaches
        // the field and inserts a newline, as does every other modifier
        // combination somebody might be mid-chord on.
        SingleActivator(LogicalKeyboardKey.enter): _SendIntent(),
        SingleActivator(LogicalKeyboardKey.numpadEnter): _SendIntent(),
      },
      child: Actions(
        actions: <Type, Action<Intent>>{
          _SendIntent: CallbackAction<_SendIntent>(onInvoke: (_) {
            onSend();
            return null;
          }),
        },
        child: child,
      ),
    );
  }
}

class _SendIntent extends Intent {
  const _SendIntent();
}
