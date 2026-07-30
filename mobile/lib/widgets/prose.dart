import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:url_launcher/url_launcher.dart';

import '../theme.dart';

/// ProseText renders assistant markdown — headers, bold/italic, inline `code`,
/// fenced code blocks, lists, blockquotes, and links — themed to match the app,
/// mirroring the TUI's markdown rendering.
class ProseText extends StatelessWidget {
  const ProseText(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) {
    return MarkdownBody(
      data: text,
      // The chat wraps content in a SelectionArea, which handles selection.
      selectable: false,
      onTapLink: (label, href, title) async {
        if (href == null) return;
        final uri = Uri.tryParse(href);
        if (uri != null) {
          await launchUrl(uri, mode: LaunchMode.externalApplication);
        }
      },
      styleSheet: MarkdownStyleSheet(
        p: T.prose,
        strong: T.proseBold,
        em: T.prose.copyWith(fontStyle: FontStyle.italic),
        a: T.prose
            .copyWith(color: T.accent, decoration: TextDecoration.underline),
        listBullet: T.prose,
        code: T.mono.copyWith(
            color: T.accentHi, backgroundColor: T.surfaceAlt, fontSize: 14),
        codeblockPadding: const EdgeInsets.all(12),
        codeblockDecoration: BoxDecoration(
          color: T.bg,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: T.border),
        ),
        blockquoteDecoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(8),
          border: const Border(left: BorderSide(color: T.accent, width: 3)),
        ),
        h1: T.proseBold.copyWith(fontSize: 22, color: T.text),
        h2: T.proseBold.copyWith(fontSize: 19, color: T.text),
        h3: T.proseBold.copyWith(fontSize: 17, color: T.accentHi),
        h4: T.proseBold.copyWith(fontSize: 15, color: T.accentHi),
      ),
    );
  }
}

/// Human-friendly long relative time ("just now", "3h ago", "2 days ago").
String relTimeLong(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inMinutes < 1) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes} min ago';
  if (d.inHours < 24) return '${d.inHours}h ago';
  if (d.inDays == 1) return 'yesterday';
  if (d.inDays < 7) return '${d.inDays} days ago';
  if (d.inDays < 30) return '${(d.inDays / 7).floor()} weeks ago';
  return '${(d.inDays / 30).floor()} months ago';
}
