import 'package:flutter/material.dart';

/// Dross mobile design tokens — the "Aetheric" identity: the bound spirit made
/// visible. Void-black surfaces with a cool blue-violet cast, an iridescent
/// violet -> cyan -> magenta aurora for anything alive (orb, send, active
/// state), and hairlines that glow faintly instead of merely dividing.
class T {
  // Surfaces (void black with a faint violet cast, no Material elevation).
  static const bg = Color(0xFF07070F); // void
  static const surface = Color(0xFF0E0E1A); // cards, input panel
  static const surfaceAlt = Color(0xFF191830); // user bubble, chips (violet cast)

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
  static const readWidth = 760.0;

  // Radii.
  static const rCard = 18.0;
  static const rBubble = 20.0;
  static const rPill = 24.0;

  // Bundled type: Inter for the app's chrome, JetBrains Mono for everything in
  // the conversation.
  //
  // The transcript is mono because it is the same transcript as the terminal —
  // the client is a view onto a session that also renders in a TUI, and prose
  // set in a serif made the two read as different products. Chrome stays sans:
  // sheets, drawers and pickers are touch UI with no terminal counterpart, and
  // setting those in mono would be costume rather than continuity.
  //
  // The serif tokens remain for anything that wants a reading face later.
  static const sans = 'Inter';
  static const serif = 'SourceSerif4';
  static const monoFamily = 'JetBrainsMono';
  static const serifFallback = <String>['Georgia', 'Charter', 'serif'];
  static const monoFallback = <String>['Menlo', 'Consolas', 'monospace'];

  /// Assistant prose in the conversation.
  static TextStyle get prose => const TextStyle(
        fontFamily: monoFamily,
        fontFamilyFallback: monoFallback,
        // Smaller than the serif it replaces: a mono face sets wider, so
        // matching its size would have shortened the line to a stub.
        fontSize: 15,
        height: 1.6,
        color: text,
      );

  /// Everything else in the conversation: prompts, event rows, activity lines.
  static const termBody = TextStyle(
      fontFamily: monoFamily,
      fontFamilyFallback: monoFallback,
      fontSize: 14.5,
      height: 1.5,
      color: text);

  /// Dim transcript text: timings, counts, the lines between turns.
  static const termMeta = TextStyle(
      fontFamily: monoFamily,
      fontFamilyFallback: monoFallback,
      fontSize: 12.5,
      height: 1.4,
      color: textDim);
  static TextStyle get proseBold =>
      prose.copyWith(fontWeight: FontWeight.w600);
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
  static const body = TextStyle(
      fontFamily: sans, fontSize: 16, height: 1.45, color: text);
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
      {super.key,
      required this.label,
      this.icon,
      this.leading,
      this.onTap});
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
      {super.key,
      required this.child,
      this.onTap,
      this.filled = false});
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
