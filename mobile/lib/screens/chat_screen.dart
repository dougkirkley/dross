import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/readable.dart';
import '../util/file_pick.dart';
import '../widgets/activity.dart';
import '../widgets/logo.dart';
import '../widgets/prose.dart';
import '../widgets/server_model_sheet.dart';

/// Conversational view of an `dross serve` session — the same look as the old
/// direct-mode chat (greeting, mono assistant prose, dark right-aligned user
/// bubbles, streaming spinner), but backed by the server. All model/provider
/// config lives on the server; the only inline control is the model chip.
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

  // Only prompts and assistant prose show in chat; tool/shell chrome is hidden.
  List<Event> _messages(List<Event> events) => events
      .where((e) => const {'prompt', 'text', 'review'}.contains(e.baseKind))
      .toList();

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final sess = s.current;
    final msgs = _messages(s.events);
    final running = sess?.isRunning ?? false;
    _autoScroll();
    return SelectionArea(
      child: Scaffold(
        backgroundColor: T.chatBg,
        body: SafeArea(
          child: Column(
            children: [
              FlatHeader(
                title: sess?.displayTitle ?? 'New chat',
                subtitle: sess?.model,
                leading: CircleIconButton(
                    icon: Icons.chevron_left,
                    onTap: () => Navigator.pop(context)),
              ),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: () => context.read<AppState>().reloadCurrent(),
                  color: T.accent,
                  backgroundColor: T.surface,
                  child: msgs.isEmpty && !running
                      // The greeting isn't scrollable on its own, so give it
                      // scroll physics or there's nothing to pull on.
                      ? Readable(
                          child: ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            children: [_greeting(sess)],
                          ),
                        )
                      : Readable(
                          child: ListView.builder(
                            controller: _scroll,
                            physics: const AlwaysScrollableScrollPhysics(),
                            padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
                            // The orb trails the conversation: lively while Dross
                            // is answering, calm (just blinking) once it's done.
                            itemCount: msgs.length + 1,
                            itemBuilder: (_, i) {
                              if (i >= msgs.length) return _spinner(running, s);
                              return _message(msgs[i]);
                            },
                          ),
                        ),
                ),
              ),
              Readable(child: _inputBar(context, s, running)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _greeting(Session? sess) => Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const DrossLogo(size: 84),
              const SizedBox(height: 20),
              Text('How can I help?',
                  style: T.prose.copyWith(fontSize: 24),
                  textAlign: TextAlign.center),
              const SizedBox(height: 8),
              Text('Ask ${sess?.model ?? 'Dross'} anything.',
                  style: T.meta, textAlign: TextAlign.center),
            ],
          ),
        ),
      );

  Widget _spinner(bool active, AppState s) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            DrossSpinner(size: 30, active: active),
            if (active) ...[
              const SizedBox(width: 10),
              // "Working…" or a more specific label (Reading…, Running…) while
              // Dross does work.
              Flexible(
                child: Text(currentActivityLabel(s.events),
                    style: T.termMeta,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
              ),
            ],
          ],
        ),
      );

  Widget _message(Event e) {
    if (e.baseKind == 'prompt') {
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
          child: Text(e.data, style: T.termBody),
        ),
      );
    }
    // assistant text / review
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: ProseText(e.data),
    );
  }

  Widget _inputBar(BuildContext context, AppState s, bool running) => SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Container(
            padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
            decoration: BoxDecoration(
              color: T.inputBg,
              borderRadius: BorderRadius.circular(T.rCard),
              border: Border.all(color: T.border),
            ),
            child: Column(
              children: [
                if (s.remotePending.isNotEmpty) _attachmentChips(context, s),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.end,
                  children: [
                    Expanded(
                      child: TextField(
                        controller: _input,
                        focusNode: _focus,
                        minLines: 1,
                        maxLines: 6,
                        style: T.termBody,
                        decoration: InputDecoration.collapsed(
                          hintText: (s.events.isNotEmpty)
                              ? 'Reply to Dross'
                              : 'Chat with Dross',
                          hintStyle: const TextStyle(color: T.textDim),
                        ),
                        onSubmitted: (_) => _send(context),
                      ),
                    ),
                    const SizedBox(width: 8),
                    _sendButton(context, running),
                  ],
                ),
                const SizedBox(height: 8),
                Row(children: [
                  _circleAction(Icons.add, () => _pickFile(context)),
                  const SizedBox(width: 8),
                  Flexible(
                    child: OutlineChip(
                      onTap: () => showServerModelSheet(context),
                      child: Text(s.current?.model ?? 'model',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style:
                              const TextStyle(color: T.textDim, fontSize: 13)),
                    ),
                  ),
                  const Spacer(),
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

  Widget _attachmentChips(BuildContext context, AppState s) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Align(
          alignment: Alignment.centerLeft,
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (int i = 0; i < s.remotePending.length; i++)
                Container(
                  padding: const EdgeInsets.fromLTRB(10, 6, 6, 6),
                  decoration: BoxDecoration(
                    color: T.surfaceAlt,
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
                  child: Row(mainAxisSize: MainAxisSize.min, children: [
                    const Icon(Icons.attach_file, size: 15, color: T.textDim),
                    const SizedBox(width: 6),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 160),
                      child: Text(s.remotePending[i].name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(color: T.text, fontSize: 13)),
                    ),
                    GestureDetector(
                      onTap: () =>
                          context.read<AppState>().removeRemotePending(i),
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

  Future<void> _pickFile(BuildContext context) async {
    final picked = await pickFile();
    if (picked == null || !context.mounted) return;
    final err = await context.read<AppState>().attachRemote(picked);
    if (err != null && context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Upload failed: $err')));
    }
  }

  Widget _sendButton(BuildContext context, bool running) => GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () =>
            running ? context.read<AppState>().interrupt() : _send(context),
        child: Container(
          width: 40,
          height: 40,
          decoration:
              const BoxDecoration(color: T.accent, shape: BoxShape.circle),
          child: Icon(running ? Icons.stop : Icons.arrow_upward,
              size: 20, color: T.bg),
        ),
      );

  Future<void> _send(BuildContext context) async {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    _input.clear();
    // Keep the text in the composer if it wasn't actually sent (e.g. the
    // keyboard-mash guard held it back), so a second tap sends the same thing.
    final sent = await context.read<AppState>().sendPrompt(text);
    if (!sent) _input.text = text;
  }
}
