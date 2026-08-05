import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:url_launcher/url_launcher.dart';

import '../theme.dart';
import 'readable.dart';

/// ProseText renders assistant markdown — headers, bold/italic, inline `code`,
/// fenced code blocks, lists, blockquotes, and links — themed to match the app,
/// mirroring the TUI's markdown rendering.
class ProseText extends StatelessWidget {
  const ProseText(this.text, {super.key});
  final String text;

  // _hoistMarker folds a lone "[Label]" first line onto its headline so the
  // status marker never renders as a bare line above its content.
  static String _hoistMarker(String s) {
    final lines = s.split('\n');
    if (lines.length < 2) return s;
    final first = lines[0].trim();
    if (!RegExp(r'^\[[^\]]+\]$').hasMatch(first)) return s;
    for (var j = 1; j < lines.length; j++) {
      final next = lines[j].trim();
      if (next.isEmpty) continue;
      if ('#-*+>`|'.contains(next[0])) return s; // don't fold a markdown block
      return (['$first $next'] + lines.sublist(j + 1)).join('\n');
    }
    return s;
  }

  @override
  Widget build(BuildContext context) {
    return MarkdownBody(
      data: _hoistMarker(text),
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
        p: proseFor(context),
        strong: proseFor(context).copyWith(fontWeight: FontWeight.w600),
        em: proseFor(context).copyWith(fontStyle: FontStyle.italic),
        a: proseFor(context)
            .copyWith(color: T.accent, decoration: TextDecoration.underline),
        listBullet: proseFor(context),
        // One style serves inline code and the text inside a fenced block, so
        // it carries no background of its own: inside a block it painted a
        // second, ragged rectangle over the block's own, line by line. The
        // monospace face is what marks inline code, and the container is what
        // marks a block.
        //
        // And not an accent colour. A page of code set in bright cyan on a
        // tinted ground is the loudest thing on screen, which is the wrong
        // order — the prose is what is being said, the code is what it refers
        // to.
        code: T.mono.copyWith(
            color: T.text, backgroundColor: Colors.transparent, fontSize: 14),
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
        // Headings differ by size and weight, as they do in a book. Colouring
        // them made every section title compete with the answer under it, and
        // a numbered list of them lit the whole page up.
        h3: T.proseBold.copyWith(fontSize: 17, color: T.text),
        h4: T.proseBold.copyWith(fontSize: 15, color: T.text),
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
