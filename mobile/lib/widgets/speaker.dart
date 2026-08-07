import 'package:flutter/material.dart';

import '../theme.dart';

/// Who a turn came from, for the rule drawn down its left.
///
/// Two, not the terminal's three: the TUI also colours a prompt that arrived
/// from a linked device, and it can, because the relay hands it the device
/// name. A session's event stream carries no such marker, so this client cannot
/// tell a prompt typed here from one typed in a terminal — and inventing the
/// distinction would be worse than not drawing it. T.remoteRule is defined
/// alongside the other two for when the stream does say.
enum Speaker {
  /// A person — whoever typed it.
  you,

  /// The agent.
  agent,
}

extension _SpeakerColor on Speaker {
  Color get color => switch (this) {
        Speaker.you => T.youRule,
        Speaker.agent => T.agentRule,
      };
}

/// A turn with a coloured rule down its left saying who is speaking.
///
/// This replaced two different ideas that were doing the same job badly. On a
/// phone a prompt was a bubble pushed to the right margin; on a desktop it was
/// a "▸ you" prefix; in the terminal it was a rule. Three answers to one
/// question, so a session open in two of them at once looked like two products.
///
/// The rule won because it is the only one of the three that survives every
/// width. A right-aligned bubble puts a request and the reply it produced at
/// opposite edges of a wide window, and a glyph prefix disappears into a long
/// paragraph — but a rule runs the full height of the turn however tall the
/// turn is, which is exactly the thing being marked.
class SpeakerTurn extends StatelessWidget {
  const SpeakerTurn({
    super.key,
    required this.speaker,
    required this.child,
    this.label,
    this.tinted = false,
    this.leading,
  });

  final Speaker speaker;
  final Widget child;

  /// Written in the rule's colour above the turn, when it is worth saying.
  ///
  /// Nothing is written beside your own prompt: the colour already says it, and
  /// "you" next to the thing you just typed is a word that tells you nothing.
  /// A remote turn keeps its device name, because which elsewhere it came from
  /// is information a colour cannot carry.
  final String? label;

  /// A faint wash behind the turn. Used for your own prompts, where the phone
  /// layout previously leaned on a bubble to say "this one is yours".
  final bool tinted;

  /// Sits before the first line — the orb, on the agent's turns.
  final Widget? leading;

  @override
  Widget build(BuildContext context) {
    final body = leading == null
        ? child
        : Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            leading!,
            const SizedBox(width: 8),
            Expanded(child: child),
          ]);
    // No outer margin: every caller already spaces its rows, and a turn that
    // padded itself as well left the rule floating short of its own text.
    return DecoratedBox(
      decoration: BoxDecoration(
        color: tinted ? T.surfaceAlt.withValues(alpha: 0.5) : null,
        // Square on the rule side so the wash reads as belonging to it rather
        // than floating beside it.
        borderRadius: const BorderRadius.horizontal(right: Radius.circular(8)),
      ),
      // IntrinsicHeight, not stretch alone. A transcript row is laid out with
      // an unbounded height — that is what a ListView gives its children — and
      // a Row that stretches its children into an unbounded constraint asks
      // for an infinite one, which is a crash rather than a tall rule. This
      // measures the text first, then gives the rule that height.
      child: IntrinsicHeight(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(width: 2, color: speaker.color),
            const SizedBox(width: 12),
            Expanded(
              child: Padding(
                padding: EdgeInsets.symmetric(
                    vertical: tinted ? 8 : 0, horizontal: tinted ? 4 : 0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (label != null && label!.isNotEmpty) ...[
                      Text(label!,
                          style: T.termMeta.copyWith(
                              color: speaker.color,
                              fontWeight: FontWeight.w600)),
                      const SizedBox(height: 3),
                    ],
                    body,
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
