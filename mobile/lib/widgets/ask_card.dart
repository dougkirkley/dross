import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';

/// A permission gate raised by the machine running this session.
///
/// The run is stopped until this is answered, which is why it sits inline in
/// the conversation with the options as buttons rather than behind a tap: the
/// thing blocking progress should be the thing you are looking at.
class AskCard extends StatefulWidget {
  const AskCard(this.data, {super.key});

  /// The raw event payload: the ask id, then the JSON question, separated by a
  /// unit separator. The id travels with it so the answer is matched to this
  /// question rather than to whatever is outstanding when it arrives.
  final String data;

  @override
  State<AskCard> createState() => _AskCardState();
}

class _AskCardState extends State<AskCard> {
  bool _sending = false;
  String? _answered;
  String? _error;

  String get _askId => widget.data.split('\x1f').first;

  Map<String, dynamic>? get _question {
    final i = widget.data.indexOf('\x1f');
    if (i < 0) return null;
    try {
      final decoded = jsonDecode(widget.data.substring(i + 1));
      final qs = (decoded is Map ? decoded['Questions'] : null) as List?;
      if (qs == null || qs.isEmpty) return null;
      return qs.first as Map<String, dynamic>;
    } catch (_) {
      return null;
    }
  }

  Future<void> _send(String answer) async {
    setState(() {
      _sending = true;
      _error = null;
    });
    final err = await context.read<AppState>().answerAsk(_askId, answer);
    if (!mounted) return;
    setState(() {
      _sending = false;
      // Only record it as answered when it actually got through — the run is
      // still blocked otherwise, and showing the choice as taken would hide
      // that.
      _answered = err == null ? answer : null;
      _error = err;
    });
  }

  @override
  Widget build(BuildContext context) {
    final q = _question;
    if (q == null) return const SizedBox.shrink();
    final options = ((q['Options'] as List?) ?? const []).cast<String>();
    final header = (q['Header'] as String?) ?? 'permission';
    final text = (q['Question'] as String?) ?? 'Allow this?';

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 10),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: T.surface,
        borderRadius: BorderRadius.circular(T.rCard),
        // Accent border: this is the one thing on screen waiting on the reader.
        border: Border.all(color: _answered == null ? T.accent : T.borderFaint),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            const Icon(Icons.lock_outline, size: 16, color: T.accent),
            const SizedBox(width: 8),
            Text(header, style: T.meta),
          ]),
          const SizedBox(height: 8),
          SelectableText(text, style: T.body),
          const SizedBox(height: 12),
          if (_answered != null)
            Text('answered: $_answered', style: T.metaFaint)
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final opt in options)
                  OutlinedButton(
                    onPressed: _sending ? null : () => _send(opt),
                    style: OutlinedButton.styleFrom(
                      foregroundColor:
                          opt.startsWith('Deny') ? T.danger : T.accent,
                      side: BorderSide(
                          color: opt.startsWith('Deny') ? T.danger : T.accent),
                    ),
                    child: Text(opt),
                  ),
              ],
            ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text('could not answer: $_error',
                style: const TextStyle(color: T.danger, fontSize: 13)),
          ],
        ],
      ),
    );
  }
}
