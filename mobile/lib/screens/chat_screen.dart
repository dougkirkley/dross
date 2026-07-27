import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../util/file_pick.dart';
import '../widgets/logo.dart';
import '../widgets/mode_sheet.dart';
import '../widgets/model_sheet.dart';

/// Direct-mode conversation. Serif assistant prose, dark right-aligned user
/// bubbles, and an editable send queue: while a reply streams, new sends stack
/// as pending items you can drop back to the input or promote to current
/// (Ctrl+C on a keyboard, matching the TUI).
class ChatScreen extends StatefulWidget {
  const ChatScreen({super.key});
  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  final _input = TextEditingController();
  final _scroll = ScrollController();
  final _focus = FocusNode();

  @override
  void dispose() {
    _input.dispose();
    _scroll.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _autoScroll() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(_scroll.position.maxScrollExtent,
            duration: const Duration(milliseconds: 180), curve: Curves.easeOut);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final chat = s.currentChat;
    _autoScroll();
    return SelectionArea(
      child: Scaffold(
      body: SafeArea(
        child: Focus(
          autofocus: true,
          onKeyEvent: (_, e) {
            // Ctrl+C promotes the head of the queue to the current turn.
            if (e is KeyDownEvent &&
                e.logicalKey == LogicalKeyboardKey.keyC &&
                HardwareKeyboard.instance.isControlPressed &&
                s.queue.isNotEmpty) {
              context.read<AppState>().promoteQueued();
              return KeyEventResult.handled;
            }
            return KeyEventResult.ignored;
          },
          child: Column(
            children: [
              FlatHeader(
                title: chat?.title ?? 'New chat',
                subtitle: s.currentModel,
                leading: CircleIconButton(
                    icon: Icons.chevron_left,
                    onTap: () => Navigator.pop(context)),
                trailing: CircleIconButton(
                    icon: Icons.more_horiz, onTap: () {}),
              ),
              Expanded(
                child: (chat == null || chat.messages.isEmpty)
                    ? _greeting(s)
                    : ListView.builder(
                        controller: _scroll,
                        padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
                        itemCount: chat.messages.length,
                        itemBuilder: (_, i) => _message(chat.messages[i]),
                      ),
              ),
              if (s.queue.isNotEmpty) _queueStrip(context, s),
              _inputBar(context, s),
            ],
          ),
        ),
      ),
    ));
  }

  Widget _greeting(AppState s) => Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const HolocronLogo(size: 84),
              const SizedBox(height: 20),
              Text('How can I help?',
                  style: T.prose.copyWith(fontSize: 24),
                  textAlign: TextAlign.center),
              const SizedBox(height: 8),
              Text('Ask ${s.currentModel} anything — no server needed.',
                  style: T.meta, textAlign: TextAlign.center),
            ],
          ),
        ),
      );

  Widget _message(ChatMessage m) {
    if (m.isUser) {
      return Align(
        alignment: Alignment.centerRight,
        child: Container(
          margin: const EdgeInsets.symmetric(vertical: 8),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          constraints: BoxConstraints(
              maxWidth: MediaQuery.of(context).size.width * 0.82),
          decoration: BoxDecoration(
            color: T.surfaceAlt,
            borderRadius: BorderRadius.circular(T.rBubble),
          ),
          child: Text(m.text, style: T.body),
        ),
      );
    }
    // assistant
    if (m.text.isEmpty && m.streaming) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: Align(
          alignment: Alignment.centerLeft,
          child: HolocronSpinner(size: 30),
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: ProseText(m.text),
    );
  }

  Widget _queueStrip(BuildContext context, AppState s) => Container(
        margin: const EdgeInsets.fromLTRB(16, 0, 16, 4),
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rCard),
          border: Border.all(color: T.border),
        ),
        child: Column(
          children: [
            for (int i = 0; i < s.queue.length; i++)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 2),
                child: Row(children: [
                  const Icon(Icons.schedule, size: 15, color: T.textDim),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(s.queue[i],
                        style: T.meta,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                  ),
                  // Promote to current (interrupt + run now).
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: const Icon(Icons.arrow_upward,
                        size: 18, color: T.accent),
                    tooltip: 'Run now',
                    onPressed: () =>
                        context.read<AppState>().promoteQueued(i),
                  ),
                  // Drop out of the queue back into the input for editing.
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: const Icon(Icons.edit_outlined,
                        size: 18, color: T.textDim),
                    tooltip: 'Edit',
                    onPressed: () {
                      final text = context.read<AppState>().removeQueued(i);
                      _input.text = text;
                      _focus.requestFocus();
                    },
                  ),
                ]),
              ),
          ],
        ),
      );

  Widget _inputBar(BuildContext context, AppState s) => SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Container(
            padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
            decoration: BoxDecoration(
              color: T.surface,
              borderRadius: BorderRadius.circular(T.rCard),
              border: Border.all(color: T.border),
            ),
            child: Column(
              children: [
                _attachmentChips(context, s),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.end,
                  children: [
                    Expanded(
                      child: TextField(
                        controller: _input,
                        focusNode: _focus,
                        minLines: 1,
                        maxLines: 6,
                        style: T.body,
                        decoration: InputDecoration.collapsed(
                          hintText: s.streaming
                              ? 'Queue after this turn…'
                              : 'Chat with Holocron',
                          hintStyle: const TextStyle(color: T.textDim),
                        ),
                        onSubmitted: (_) => _send(context),
                      ),
                    ),
                    const SizedBox(width: 8),
                    _sendButton(context, s),
                  ],
                ),
                const SizedBox(height: 8),
                Row(children: [
                  // Attach a file.
                  _circleAction(Icons.add, () => _pickFile(context)),
                  const SizedBox(width: 8),
                  // Permission-mode selector (opens the Select mode sheet).
                  OutlineChip(
                    onTap: () => showModeSheet(context),
                    child: Row(mainAxisSize: MainAxisSize.min, children: [
                      const Icon(Icons.bolt, size: 15, color: T.accentHi),
                      const SizedBox(width: 6),
                      Text(s.permissionMode.label,
                          style: const TextStyle(color: T.text, fontSize: 13)),
                    ]),
                  ),
                  const SizedBox(width: 8),
                  Flexible(
                    child: OutlineChip(
                      onTap: () => showModelSheet(context),
                      child: Text(s.currentModel,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(color: T.textDim, fontSize: 13)),
                    ),
                  ),
                  const Spacer(),
                  if (s.streaming)
                    IconButton(
                      icon: const Icon(Icons.stop_circle_outlined,
                          color: T.textDim),
                      tooltip: 'Interrupt',
                      onPressed: () =>
                          context.read<AppState>().interruptDirect(),
                    ),
                ]),
              ],
            ),
          ),
        ),
      );

  Widget _circleAction(IconData icon, VoidCallback onTap) => GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          width: 38,
          height: 38,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: T.border),
          ),
          child: Icon(icon, size: 20, color: T.text),
        ),
      );

  Widget _attachmentChips(BuildContext context, AppState s) {
    if (s.pending.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Align(
        alignment: Alignment.centerLeft,
        child: Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (int i = 0; i < s.pending.length; i++)
              Container(
                padding: const EdgeInsets.fromLTRB(10, 6, 6, 6),
                decoration: BoxDecoration(
                  color: T.surfaceAlt,
                  borderRadius: BorderRadius.circular(T.rPill),
                ),
                child: Row(mainAxisSize: MainAxisSize.min, children: [
                  Icon(s.pending[i].isImage ? Icons.image_outlined : Icons.description_outlined,
                      size: 15, color: T.textDim),
                  const SizedBox(width: 6),
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 160),
                    child: Text(s.pending[i].name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(color: T.text, fontSize: 13)),
                  ),
                  GestureDetector(
                    onTap: () => context.read<AppState>().removeAttachment(i),
                    child: const Padding(
                      padding: EdgeInsets.only(left: 4),
                      child: Icon(Icons.close, size: 16, color: T.textDim),
                    ),
                  ),
                ]),
              ),
          ],
        ),
      ),
    );
  }

  Future<void> _pickFile(BuildContext context) async {
    final picked = await pickFile();
    if (picked == null) return;
    final bytes = picked.bytes;
    final mime = _mimeFromName(picked.name);
    String? text;
    if (bytes.isNotEmpty && bytes.length < 256 * 1024 && !mime.startsWith('image/')) {
      try {
        text = utf8.decode(bytes);
      } catch (_) {
        text = null; // binary
      }
    }
    if (context.mounted) {
      context.read<AppState>().addAttachment(
          Attachment(name: picked.name, bytes: bytes, mime: mime, text: text));
    }
  }

  String _mimeFromName(String name) {
    final ext = name.contains('.') ? name.split('.').last.toLowerCase() : '';
    return switch (ext) {
      'png' => 'image/png',
      'jpg' || 'jpeg' => 'image/jpeg',
      'gif' => 'image/gif',
      'webp' => 'image/webp',
      'json' => 'application/json',
      'md' => 'text/markdown',
      'txt' || 'log' => 'text/plain',
      _ => '',
    };
  }

  Widget _sendButton(BuildContext context, AppState s) => GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () => _send(context),
        child: Container(
          width: 40,
          height: 40,
          decoration: const BoxDecoration(
              color: T.accent, shape: BoxShape.circle),
          child: Icon(s.streaming ? Icons.add : Icons.arrow_upward,
              size: 20, color: T.bg),
        ),
      );

  void _send(BuildContext context) {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    _input.clear();
    context.read<AppState>().sendDirect(text);
  }
}


/// A light markdown-ish renderer for assistant prose: serif body, monospace
/// fenced code blocks and inline `code`, and **bold** spans. Not a full
/// markdown engine — just enough to read like Claude's chat.
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
