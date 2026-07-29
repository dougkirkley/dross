import 'package:flutter/material.dart';

import '../theme.dart';

/// ProseText renders lightweight markdown (fenced code blocks, inline `code`,
/// and **bold**) as serif prose — used for assistant text in sessions.
class ProseText extends StatelessWidget {
  const ProseText(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) {
    final blocks = <Widget>[];
    final parts = text.split('```');
    for (var i = 0; i < parts.length; i++) {
      final chunk = parts[i];
      if (chunk.isEmpty) continue;
      if (i.isOdd) {
        // fenced code block; drop an optional language line
        var code = chunk;
        final nl = code.indexOf('\n');
        if (nl != -1 && !code.substring(0, nl).contains(' ')) {
          code = code.substring(nl + 1);
        }
        blocks.add(Container(
          width: double.infinity,
          margin: const EdgeInsets.symmetric(vertical: 8),
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: T.bg,
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: T.border),
          ),
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Text(code.trimRight(),
                style: T.mono.copyWith(color: T.text, height: 1.4)),
          ),
        ));
      } else {
        blocks.add(RichText(text: _inline(chunk)));
      }
    }
    return Column(
        crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
  }

  /// Builds spans for inline `code` and **bold** within serif prose.
  TextSpan _inline(String s) {
    final spans = <TextSpan>[];
    final re = RegExp(r'`([^`]+)`|\*\*([^*]+)\*\*');
    var last = 0;
    for (final m in re.allMatches(s)) {
      if (m.start > last) {
        spans.add(TextSpan(text: s.substring(last, m.start), style: T.prose));
      }
      if (m.group(1) != null) {
        spans.add(TextSpan(
            text: m.group(1),
            style: T.mono.copyWith(
                color: T.text, backgroundColor: T.surfaceAlt, fontSize: 15)));
      } else {
        spans.add(TextSpan(text: m.group(2), style: T.proseBold));
      }
      last = m.end;
    }
    if (last < s.length) {
      spans.add(TextSpan(text: s.substring(last), style: T.prose));
    }
    return TextSpan(children: spans);
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
