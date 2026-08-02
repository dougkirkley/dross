import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';

/// One question inside an ask.
class _Question {
  _Question(this.header, this.text, this.options, this.multi);
  final String header;
  final String text;
  final List<String> options;
  final bool multi;

  /// No options means the model wants prose back, not a choice.
  bool get freeText => options.isEmpty;

  // The wire keys are the Go struct's JSON tags: lower-case, except the
  // Questions list itself, which has no tag and so keeps its field name.
  static _Question? parse(Object? raw) {
    if (raw is! Map) return null;
    return _Question(
      (raw['header'] as String?) ?? '',
      (raw['question'] as String?) ?? '',
      ((raw['options'] as List?) ?? const []).whereType<String>().toList(),
      raw['multiSelect'] == true,
    );
  }
}

/// A question from the machine running this session: a permission gate, or the
/// agent asking something it needs to know before it can go on.
///
/// The run is stopped until this is answered, so it sits inline in the
/// conversation rather than behind a tap — the thing blocking progress should
/// be the thing you are looking at.
class AskCard extends StatefulWidget {
  const AskCard(this.data, {super.key});

  /// The ask id, then the JSON request, separated by a unit separator. The id
  /// travels with it so the answer is matched to this question rather than to
  /// whatever is outstanding when it lands.
  final String data;

  @override
  State<AskCard> createState() => _AskCardState();
}

class _AskCardState extends State<AskCard> {
  bool _sending = false;
  String? _answered;
  String? _error;

  /// Chosen options per question; multi-select keeps several.
  final Map<int, Set<String>> _picked = {};
  final Map<int, TextEditingController> _typed = {};

  @override
  void dispose() {
    for (final c in _typed.values) {
      c.dispose();
    }
    super.dispose();
  }

  String get _askId => widget.data.split('\x1f').first;

  List<_Question> get _questions {
    final i = widget.data.indexOf('\x1f');
    if (i < 0) return const [];
    try {
      final decoded = jsonDecode(widget.data.substring(i + 1));
      final raw = (decoded is Map ? decoded['Questions'] : null) as List?;
      if (raw == null) return const [];
      return raw.map(_Question.parse).whereType<_Question>().toList();
    } catch (_) {
      return const [];
    }
  }

  String _answerFor(int i, _Question q) =>
      q.freeText ? _typed[i]?.text.trim() ?? '' : (_picked[i] ?? {}).join(', ');

  bool _complete(List<_Question> qs) {
    for (var i = 0; i < qs.length; i++) {
      if (_answerFor(i, qs[i]).isEmpty) return false;
    }
    return true;
  }

  /// Matches what the terminal sends, because the agent parses both the same
  /// way: a lone question answers with the choice alone — approve() compares it
  /// to the offered option exactly — and several questions are labelled so the
  /// answers can be told apart.
  String _compose(List<_Question> qs) {
    if (qs.length == 1) return _answerFor(0, qs[0]);
    return [
      for (var i = 0; i < qs.length; i++)
        '${qs[i].header.isEmpty ? qs[i].text : qs[i].header}: ${_answerFor(i, qs[i])}',
    ].join('\n');
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
      // Only settled once it got through. The run is still blocked otherwise,
      // and showing the choice as taken would hide that.
      _answered = err == null ? answer : null;
      _error = err;
    });
  }

  @override
  Widget build(BuildContext context) {
    final qs = _questions;
    if (qs.isEmpty) return const SizedBox.shrink();
    final single = qs.length == 1;

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 10),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: T.surface,
        borderRadius: BorderRadius.circular(T.rCard),
        // Accent while it waits: this is the one thing holding up the run.
        border: Border.all(color: _answered == null ? T.accent : T.borderFaint),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            const Icon(Icons.help_outline, size: 16, color: T.accent),
            const SizedBox(width: 8),
            Text(qs.first.header.isEmpty ? 'question' : qs.first.header,
                style: T.meta),
          ]),
          const SizedBox(height: 10),
          if (_answered != null)
            Text('answered: $_answered', style: T.metaFaint)
          else ...[
            for (var i = 0; i < qs.length; i++) _questionBlock(i, qs[i]),
            // A lone choice sends the moment it is tapped; anything else needs
            // a Send, because the answer is not complete until every question
            // has one.
            if (!single || qs.first.freeText || qs.first.multi) ...[
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerRight,
                child: FilledButton(
                  onPressed: _sending || !_complete(qs)
                      ? null
                      : () => _send(_compose(qs)),
                  child: const Text('Send'),
                ),
              ),
            ],
          ],
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text('could not answer: $_error',
                style: const TextStyle(color: T.danger, fontSize: 13)),
          ],
        ],
      ),
    );
  }

  Widget _questionBlock(int i, _Question q) {
    final single = _questions.length == 1;
    return Padding(
      padding: EdgeInsets.only(bottom: i == _questions.length - 1 ? 0 : 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (q.text.isNotEmpty) SelectableText(q.text, style: T.body),
          const SizedBox(height: 10),
          if (q.freeText)
            TextField(
              controller: _typed.putIfAbsent(i, TextEditingController.new),
              enabled: !_sending,
              minLines: 1,
              maxLines: 4,
              style: T.body,
              onChanged: (_) => setState(() {}), // re-evaluate Send
              decoration: const InputDecoration(
                hintText: 'type your answer…',
                border: OutlineInputBorder(),
                isDense: true,
              ),
            )
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final opt in q.options) _optionChip(i, q, opt, single),
              ],
            ),
        ],
      ),
    );
  }

  Widget _optionChip(int i, _Question q, String opt, bool single) {
    final chosen = (_picked[i] ?? const {}).contains(opt);
    final deny = opt.startsWith('Deny');
    final colour = deny ? T.danger : T.accent;

    void choose() {
      setState(() {
        final set = _picked.putIfAbsent(i, () => <String>{});
        if (q.multi) {
          chosen ? set.remove(opt) : set.add(opt);
        } else {
          set
            ..clear()
            ..add(opt);
        }
      });
      // A single-select lone question is the permission-gate shape: one tap is
      // the whole answer, so waiting for a Send would just be a second tap.
      if (single && !q.multi) _send(opt);
    }

    return OutlinedButton(
      onPressed: _sending ? null : choose,
      style: OutlinedButton.styleFrom(
        foregroundColor: colour,
        side: BorderSide(color: colour),
        backgroundColor: chosen ? colour.withValues(alpha: 0.15) : null,
      ),
      child: Text(opt),
    );
  }
}
