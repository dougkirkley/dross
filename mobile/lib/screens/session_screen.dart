import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';

// Chat-like view of a session's live event stream, with an input bar. Mirrors
// the TUI: tool calls, delegation (worker events indented), cost in the bar.
class SessionScreen extends StatefulWidget {
  const SessionScreen({super.key});

  @override
  State<SessionScreen> createState() => _SessionScreenState();
}

class _SessionScreenState extends State<SessionScreen> {
  final _input = TextEditingController();
  final _scroll = ScrollController();

  @override
  void dispose() {
    _input.dispose();
    _scroll.dispose();
    // Keep the session attached in AppState so it streams in the background;
    // detach only if you want to stop. Here we leave it attached.
    super.dispose();
  }

  void _autoScroll() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(_scroll.position.maxScrollExtent,
            duration: const Duration(milliseconds: 200), curve: Curves.easeOut);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final sess = s.current;
    _autoScroll();
    return Scaffold(
      appBar: AppBar(
        title: Text(sess?.model ?? 'session'),
        actions: [
          if (sess?.isRunning ?? false)
            IconButton(
              icon: const Icon(Icons.stop_circle_outlined),
              tooltip: 'Interrupt',
              onPressed: () => context.read<AppState>().interrupt(),
            ),
          Center(
            child: Padding(
              padding: const EdgeInsets.only(right: 12),
              child: Text('\$${(sess?.cost ?? 0).toStringAsFixed(4)}',
                  style: const TextStyle(color: Color(0xFF8FC7E8))),
            ),
          ),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: ListView.builder(
              controller: _scroll,
              padding: const EdgeInsets.all(12),
              itemCount: s.events.length,
              itemBuilder: (_, i) => _eventRow(s.events[i]),
            ),
          ),
          if (sess?.isRunning ?? false) const LinearProgressIndicator(minHeight: 2),
          _inputBar(context),
        ],
      ),
    );
  }

  Widget _eventRow(Event e) {
    final indent = e.isWorker ? const EdgeInsets.only(left: 20) : EdgeInsets.zero;
    Widget content;
    switch (e.baseKind) {
      case 'prompt':
        content = _line('you', e.data, const Color(0xFF6B7280), bold: true);
        break;
      case 'text':
        content = _line('holocron', e.data, const Color(0xFF4B9CD3), bold: true);
        break;
      case 'tool':
        content = _mono('→ ${e.data}', const Color(0xFF8FC7E8));
        break;
      case 'tool-result':
        content = _mono(e.data, const Color(0xFF4B5158), max: 3);
        break;
      case 'checkpoint':
        content = _mono('✓ ${e.data}', const Color(0xFF8FC7E8));
        break;
      case 'error':
        content = _mono('✗ ${e.data}', const Color(0xFFE06C75));
        break;
      case 'done':
        content = _mono('done · ${e.data}', const Color(0xFF6B7280));
        break;
      default:
        content = _mono(e.data, const Color(0xFF9AA0A6));
    }
    return Padding(padding: EdgeInsets.symmetric(vertical: 3).add(indent), child: content);
  }

  Widget _line(String label, String text, Color color, {bool bold = false}) => RichText(
        text: TextSpan(children: [
          TextSpan(
            text: '$label  ',
            style: TextStyle(color: color, fontWeight: bold ? FontWeight.bold : null, fontFamily: 'monospace'),
          ),
          TextSpan(text: text, style: const TextStyle(color: Color(0xFFE6E6E6), fontFamily: 'monospace')),
        ]),
      );

  Widget _mono(String text, Color color, {int? max}) => Text(
        text,
        maxLines: max,
        overflow: max != null ? TextOverflow.ellipsis : TextOverflow.clip,
        style: TextStyle(color: color, fontFamily: 'monospace', fontSize: 13),
      );

  Widget _inputBar(BuildContext context) => SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(8),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _input,
                  minLines: 1,
                  maxLines: 4,
                  decoration: InputDecoration(
                    hintText: 'ask holocron…',
                    border: OutlineInputBorder(borderRadius: BorderRadius.circular(12)),
                    contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                  ),
                  onSubmitted: (_) => _send(context),
                ),
              ),
              IconButton(
                icon: const Icon(Icons.send, color: Color(0xFF4B9CD3)),
                onPressed: () => _send(context),
              ),
            ],
          ),
        ),
      );

  void _send(BuildContext context) {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    _input.clear();
    context.read<AppState>().sendPrompt(text);
  }
}
