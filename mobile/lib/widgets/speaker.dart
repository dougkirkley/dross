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

/// A turn, marked as its speaker's only where the marking earns its space.
///
/// There used to be a coloured rule down the left of every turn, chosen over a
/// right-aligned bubble and a "▸ you" prefix because it is the one marker that
/// survives every window width. That reasoning was sound and the conclusion
/// still wrong, for a reason a wide window does not show: on a phone, a working
/// session is mostly short turns, and a rule down each of them is a column of
/// coloured dashes beside two lines of text — the marker taking as much of the
/// eye as the thing marked, over and over.
///
/// So nothing is drawn for the agent, which is the overwhelming majority of the
/// turns and needs no marking to be understood as the reply. A prompt keeps its
/// [tinted] wash, which says "you said this" without a second element: it is
/// the same trick a message thread uses, and it costs no width.
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
    // No outer margin: every caller already spaces its rows.
    //
    // No IntrinsicHeight either, which the rule needed and nothing else does. A
    // transcript row is laid out with an unbounded height, so a Row stretching
    // its children into it asked for an infinite one — a crash rather than a
    // tall rule — and measuring the text twice to avoid that was the price of
    // the rule alone.
    return DecoratedBox(
      decoration: BoxDecoration(
        color: tinted ? T.surfaceAlt.withValues(alpha: 0.5) : null,
        // Rounded on both sides now. It was square on the left so the wash read
        // as belonging to the rule beside it; with no rule there, a square edge
        // is just a bubble with a corner missing.
        borderRadius: BorderRadius.circular(10),
      ),
      child: Padding(
        // A tinted turn is a bubble and needs room inside it. An untinted one
        // is the reply, and pads to nothing so prose runs the full width.
        padding: EdgeInsets.symmetric(
            vertical: tinted ? 10 : 0, horizontal: tinted ? 12 : 0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (label != null && label!.isNotEmpty) ...[
              Text(label!,
                  style: T.termMeta.copyWith(
                      color: speaker.color, fontWeight: FontWeight.w600)),
              const SizedBox(height: 3),
            ],
            body,
          ],
        ),
      ),
    );
  }
}
