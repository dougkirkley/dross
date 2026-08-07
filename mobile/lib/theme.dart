import 'package:flutter/material.dart';

/// Dross mobile design tokens — the "Aetheric" identity: the bound spirit made
/// visible. Void-black surfaces with a cool blue-violet cast, an iridescent
/// violet -> cyan -> magenta aurora for anything alive (orb, send, active
/// state), and hairlines that glow faintly instead of merely dividing.
class T {
  // Surfaces (void black with a faint violet cast, no Material elevation).
  static const bg = Color(0xFF07070F); // void
  static const surface = Color(0xFF0E0E1A); // cards, input panel
  static const surfaceAlt =
      Color(0xFF191830); // user bubble, chips (violet cast)

  // Conversation surfaces: the message area lifts slightly off the void, the
  // input field sits darker for contrast.
  static const chatBg = Color(0xFF0A0A14);
  static const inputBg = Color(0xFF08080F);
  static const border = Color(0xFF241E3B); // violet-tinted hairline
  static const borderFaint = Color(0xFF161426);

  // Text.
  static const text = Color(0xFFECE9FB); // cool off-white
  static const textDim = Color(0xFF7D7BA3);
  static const textFaint = Color(0xFF55547A);

  // Accent — aetheric violet, with cyan as the second aurora pole.
  static const accent = Color(0xFFA78BFA);
  static const accentHi = Color(0xFF22D3EE);
  static const bloom = Color(0xFFE879F9); // magenta aurora tail
  static const violet = Color(0xFF8B5CF6);

  // The living gradient: used for the orb, the send control, active frames,
  // and glowing hairlines.
  static const aurora = <Color>[violet, accentHi, bloom];
  static const LinearGradient auroraGrad = LinearGradient(
    colors: aurora,
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );
  static const LinearGradient auroraLine = LinearGradient(colors: aurora);

  // Status / semantic.
  static const ok = Color(0xFF34D399);
  static const danger = Color(0xFFFB7185);
  static const gold = Color(0xFFE6B450);

  // Who is speaking, said with a colour down the left of the turn.
  //
  // The same three colours the terminal uses, deliberately: a session can be
  // read in the TUI, in the browser and on a phone, often two of them at once,
  // and the thing that tells you whose turn you are looking at should not
  // change between them. Violet for the agent, the accent for you, gold for a
  // turn that arrived from a linked device.
  static const youRule = accent;
  static const agentRule = violet;
  static const remoteRule = gold;

  // Light pill (the "New session/chat" FAB is near-white with dark text).
  static const pill = Color(0xFFF3F1EC);
  static const pillText = Color(0xFF0E0E0E);

  /// The widest the conversation is allowed to get.
  ///
  /// The same client runs on a phone and in a desktop browser, and left to
  /// itself the column stretches to the window: on a wide screen a reply
  /// spanned the full width, which is a tiring line length to read and threw
  /// the user's bubbles and the agent's text to opposite edges. Roughly 90
  /// characters of body text, centred, with the phone unaffected because it is
  /// narrower than this anyway.
  /// The column everything on a conversation screen sits in — transcript and
  /// composer alike.
  ///
  /// One number, not two. The composer was briefly given its own, wider cap,
  /// which made the input visibly disagree with the messages above it: two
  /// different left edges on the same screen reads as a mistake rather than as
  /// a decision about line length. Matching edges is worth more here than the
  /// slightly shorter prose lines a narrower transcript would give.
  static const readWidth = 1100.0;

  /// Above this the conversation takes the desktop treatment: the transcript
  /// laid out like the terminal, with gutter glyphs instead of chat bubbles.
  ///
  /// A width rather than a platform check. The browser is the case that wants
  /// it, but a browser window dragged narrow wants the touch layout back, and
  /// asking "is this the web?" would give a phone-shaped window the wrong one.
  static const wideAt = 700.0;

  // Radii.
  static const rCard = 18.0;
  static const rBubble = 20.0;
  static const rPill = 24.0;

  // Bundled type: Inter for chrome, a serif for reading, JetBrains Mono for
  // code and for the desktop transcript.
  //
  // Which one the conversation uses depends on the width it is being read at.
  // On a desktop the browser sits beside a terminal rendering the same session,
  // and mono keeps the two reading as one product. On a phone there is no
  // terminal to match and the thread is read like messages, so it takes the
  // serif. Chrome stays sans at both sizes.
  static const sans = 'Inter';
  static const serif = 'SourceSerif4';
  static const monoFamily = 'JetBrainsMono';
  static const serifFallback = <String>['Georgia', 'Charter', 'serif'];
  static const monoFallback = <String>['Menlo', 'Consolas', 'monospace'];

  /// Assistant prose on a phone: a reading face, because a phone conversation
  /// is read like a message thread and has no terminal beside it to match.
  static TextStyle get prose => const TextStyle(
        fontFamily: serif,
        fontFamilyFallback: serifFallback,
        fontSize: 19,
        height: 1.55,
        letterSpacing: 0.1,
        color: text,
      );

  /// Assistant prose at desktop widths.
  ///
  /// A reading face, not the terminal's. This was mono, to match the TUI
  /// rendering the same session in the next window — which is a nice idea
  /// about two windows and a poor one about the paragraph actually being
  /// read. A monospaced face gives every letter the same width, so word
  /// shapes flatten and long prose is markedly harder going; that is why no
  /// tool sets a chat transcript in one. Code, diffs and tool output keep
  /// mono, where character alignment is the point.
  static TextStyle get termProse => const TextStyle(
        fontFamily: serif,
        fontFamilyFallback: serifFallback,
        fontSize: 17.5,
        height: 1.6,
        letterSpacing: 0.1,
        color: text,
      );

  /// Everything else in the conversation: prompts, event rows, activity lines.
  static const termBody =
      TextStyle(fontFamily: sans, fontSize: 15.5, height: 1.5, color: text);

  /// Dim transcript text: timings, counts, the lines between turns.
  ///
  /// textDim rather than the fainter grey: these sit against a near-black
  /// background at 13px, and one step too far down stops being quiet and
  /// starts being unreadable.
  static const termMeta =
      TextStyle(fontFamily: sans, fontSize: 13, height: 1.4, color: textDim);
  static TextStyle get proseBold => prose.copyWith(fontWeight: FontWeight.w600);
  static const title = TextStyle(
      fontFamily: sans,
      fontSize: 21,
      fontWeight: FontWeight.w600,
      letterSpacing: -0.2,
      color: text);
  static const rowTitle = TextStyle(
      fontFamily: sans,
      fontSize: 18,
      fontWeight: FontWeight.w600,
      letterSpacing: -0.1,
      color: text);
  static const body =
      TextStyle(fontFamily: sans, fontSize: 16, height: 1.45, color: text);
  static const meta =
      TextStyle(fontFamily: sans, fontSize: 13.5, color: textDim);
  static const metaFaint =
      TextStyle(fontFamily: sans, fontSize: 13.5, color: textFaint);
  static const mono = TextStyle(
      fontFamily: monoFamily,
      fontFamilyFallback: monoFallback,
      fontSize: 13,
      color: textDim);

  /// A dark, flat Material theme stripped of ripples/elevation so custom
  /// widgets read as the app's own design rather than stock Material.
  static ThemeData themeData() {
    const scheme = ColorScheme.dark(
      surface: bg,
      primary: accent,
      secondary: accentHi,
      error: danger,
    );
    return ThemeData(
      useMaterial3: true,
      fontFamily: sans,
      colorScheme: scheme,
      scaffoldBackgroundColor: bg,
      canvasColor: bg,
      splashFactory: NoSplash.splashFactory,
      highlightColor: Colors.transparent,
      splashColor: Colors.transparent,
      dividerColor: border,
      textSelectionTheme: const TextSelectionThemeData(cursorColor: accent),
    );
  }
}

/// A 1px hairline that glows with the aurora gradient, fading at both edges.
/// The signature Aetheric divider — used under headers and around live frames.
class GradientHairline extends StatelessWidget {
  const GradientHairline({super.key, this.height = 1});
  final double height;

  @override
  Widget build(BuildContext context) => Container(
        height: height,
        decoration: const BoxDecoration(
          gradient: LinearGradient(colors: [
            Colors.transparent,
            T.violet,
            T.accentHi,
            T.bloom,
            Colors.transparent,
          ]),
        ),
      );
}

/// A circular hairline-outlined icon button, as used in the Claude app headers
/// (back chevron, menu, filter).
class CircleIconButton extends StatelessWidget {
  const CircleIconButton(
      {super.key, required this.icon, this.onTap, this.tooltip});
  final IconData icon;
  final VoidCallback? onTap;
  final String? tooltip;

  @override
  Widget build(BuildContext context) {
    final b = GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Container(
        width: 44,
        height: 44,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          border: Border.all(color: T.border),
        ),
        child: Icon(icon, size: 20, color: T.text),
      ),
    );
    return tooltip == null ? b : Tooltip(message: tooltip!, child: b);
  }
}

/// The near-white pill button pinned bottom-right ("New session" / "New chat").
class PillButton extends StatelessWidget {
  const PillButton(
      {super.key, required this.label, this.icon, this.leading, this.onTap});
  final String label;
  final IconData? icon;

  /// Optional custom leading widget, used where a brand mark (e.g. the Google
  /// "G") should appear instead of a Material icon.
  final Widget? leading;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
        decoration: BoxDecoration(
          color: T.pill,
          borderRadius: BorderRadius.circular(T.rPill),
        ),
        child: Row(mainAxisSize: MainAxisSize.min, children: [
          leading ?? Icon(icon, size: 20, color: T.pillText),
          const SizedBox(width: 8),
          Text(label,
              style: const TextStyle(
                  color: T.pillText,
                  fontSize: 16,
                  fontWeight: FontWeight.w600)),
        ]),
      ),
    );
  }
}

/// A small outlined chip (permission-mode selector, "Create PR", model picker).
class OutlineChip extends StatelessWidget {
  const OutlineChip(
      {super.key, required this.child, this.onTap, this.filled = false});
  final Widget child;
  final VoidCallback? onTap;
  final bool filled;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          color: filled ? T.surfaceAlt : Colors.transparent,
          borderRadius: BorderRadius.circular(T.rPill),
          border: Border.all(color: T.border),
        ),
        child: child,
      ),
    );
  }
}

/// A flat header row (title centered, optional leading/trailing) replacing the
/// Material AppBar chrome.
class FlatHeader extends StatelessWidget {
  const FlatHeader(
      {super.key,
      required this.title,
      this.subtitle,
      this.leading,
      this.trailing});
  final String title;
  final String? subtitle;
  final Widget? leading;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
          child: Row(
            children: [
              SizedBox(width: 44, child: leading),
              Expanded(
                child: Column(
                  children: [
                    Text(title,
                        style: T.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        textAlign: TextAlign.center),
                    if (subtitle != null)
                      Text(subtitle!,
                          style: T.meta, textAlign: TextAlign.center),
                  ],
                ),
              ),
              SizedBox(width: 44, child: trailing),
            ],
          ),
        ),
        const GradientHairline(),
      ],
    );
  }
}

/// A light bar that sweeps back and forth: a session is working.
///
/// Indeterminate on purpose. A run has no measurable progress — the agent is
/// taking as many turns as it needs — so a bar that filled left to right would
/// be inventing one. This says only "still going", which is the whole of what
/// is known.
///
/// It sits on the top edge of the card it belongs to, using the same aurora
/// colours as GradientHairline so it reads as part of the same surface rather
/// than as a stock progress indicator.
class RunningBar extends StatefulWidget {
  const RunningBar({super.key, this.height = 2});
  final double height;

  @override
  State<RunningBar> createState() => _RunningBarState();
}

class _RunningBarState extends State<RunningBar>
    with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1300),
  )..repeat(reverse: true);

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: widget.height,
      child: LayoutBuilder(builder: (context, box) {
        final w = box.maxWidth;
        final seg = w * 0.35;
        return AnimatedBuilder(
          animation: _c,
          builder: (_, __) {
            // easeInOut so it slows at each end rather than snapping back,
            // which is what makes it read as a sweep and not a jump.
            final t = Curves.easeInOut.transform(_c.value);
            return Stack(children: [
              Positioned(
                left: (w - seg) * t,
                width: seg,
                top: 0,
                bottom: 0,
                child: Container(
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(widget.height),
                    gradient: const LinearGradient(colors: [
                      Colors.transparent,
                      T.violet,
                      T.accentHi,
                      T.bloom,
                      Colors.transparent,
                    ]),
                  ),
                ),
              ),
            ]);
          },
        );
      }),
    );
  }
}
