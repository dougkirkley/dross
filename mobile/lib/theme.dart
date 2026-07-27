import 'package:flutter/material.dart';

/// Holocron mobile design tokens. The layout language follows Claude's phone app
/// — flat warm near-black surfaces, hairline borders, serif assistant prose,
/// dark right-aligned user bubbles, pill controls — but keeps holocron's own
/// Carolina-blue accent where Claude uses coral, for brand identity.
class T {
  // Surfaces (warm near-black, no Material elevation).
  static const bg = Color(0xFF0E0E0E);
  static const surface = Color(0xFF191919); // cards, input
  static const surfaceAlt = Color(0xFF222222); // user bubble, chips

  // Conversation surfaces: the message area reads as a soft gray, the input
  // field sits darker (near-black) for contrast.
  static const chatBg = Color(0xFF1A1A1A);
  static const inputBg = Color(0xFF0C0C0C);
  static const border = Color(0xFF2A2A2A); // hairline
  static const borderFaint = Color(0xFF1E1E1E);

  // Text.
  static const text = Color(0xFFECEAE4); // warm off-white
  static const textDim = Color(0xFF9A9A93);
  static const textFaint = Color(0xFF6A6A64);

  // Accent — holocron Carolina blue.
  static const accent = Color(0xFF4B9CD3);
  static const accentHi = Color(0xFF8FC7E8);

  // Status / semantic.
  static const ok = Color(0xFF6BBF73);
  static const danger = Color(0xFFE06C75);

  // Light pill (the "New session/chat" FAB is near-white with dark text).
  static const pill = Color(0xFFF3F1EC);
  static const pillText = Color(0xFF0E0E0E);

  // Radii.
  static const rCard = 18.0;
  static const rBubble = 20.0;
  static const rPill = 24.0;

  // Bundled type: Inter for UI, Newsreader (Tiempos-like) for assistant prose,
  // JetBrains Mono for code. Fallbacks resolve to platform faces if an asset
  // fails to load.
  static const sans = 'Inter';
  static const serif = 'Newsreader';
  static const monoFamily = 'JetBrainsMono';
  static const serifFallback = <String>['Georgia', 'Charter', 'serif'];
  static const monoFallback = <String>['Menlo', 'Consolas', 'monospace'];

  static TextStyle get prose => const TextStyle(
        fontFamily: serif,
        fontFamilyFallback: serifFallback,
        fontSize: 19,
        height: 1.55,
        letterSpacing: 0.1,
        color: text,
      );
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
      {super.key, required this.label, required this.icon, this.onTap});
  final String label;
  final IconData icon;
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
          Icon(icon, size: 20, color: T.pillText),
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
    return Padding(
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
    );
  }
}
