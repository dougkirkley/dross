import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/readable.dart';
import '../util/file_pick.dart';
import '../widgets/activity.dart';
import '../widgets/follow_bottom.dart';
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
  final _follow = FollowBottom();
  final _focus = FocusNode();

  /// Stop has been asked for and the run has not ended yet.
  ///
  /// The server accepts the request and the turn ends when the model call
  /// unwinds, which is not instant. Without this the UI is unchanged in the
  /// meantime — same Stop button, same "Working…" — so the only reading
  /// available is that the button does nothing.
  bool _stopping = false;

  @override
  void dispose() {
    _input.dispose();
    _follow.dispose();
    _focus.dispose();
    super.dispose();
  }

  List<Event> _messages(List<Event> events) => chatMessages(events);

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final sess = s.current;
    final msgs = _messages(s.events);
    final running = sess?.isRunning ?? false;
    // The run ended, however it ended: the stop request is no longer pending.
    if (!running && _stopping) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) setState(() => _stopping = false);
      });
    }
    _follow.follow();
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
                            controller: _follow.controller,
                            physics: const AlwaysScrollableScrollPhysics(),
                            padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
                            // After the messages: the answer as it streams in,
                            // then the orb — lively while Dross is answering,
                            // calm (just blinking) once it's done.
                            itemCount: msgs.length + 2,
                            itemBuilder: (_, i) {
                              if (i == msgs.length) return _streaming(s);
                              if (i > msgs.length) return _spinner(running, s);
                              return _message(msgs[i]);
                            },
                          ),
                        ),
                ),
              ),
              // Offered only once the reader has left the end of the thread,
              // and only when there is a thread to have left.
              if (msgs.isNotEmpty || running)
                AnimatedBuilder(
                  animation: _follow,
                  builder: (_, __) => JumpToLatest(
                      follow: _follow, label: running ? 'Writing' : 'Latest'),
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

  /// The answer while it is being written — the same prose block the final
  /// "text" event will replace, growing a word at a time. This, not the
  /// spinner, is what makes a local model feel alive: the tokens were always
  /// being produced during those forty-five silent seconds, they were just
  /// being buffered into one dump.
  Widget _streaming(AppState s) {
    if (s.streamingText.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: ProseText(s.streamingText),
    );
  }

  Widget _spinner(bool active, AppState s) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            DrossSpinner(size: 30, active: active),
            if (active) ...[
              const SizedBox(width: 10),
              // "Working…" or a more specific label (Reading…, Running…) while
              // Dross does work — and "Stopping…" once Stop has been asked for,
              // because until the turn actually ends that request is the most
              // recent true thing about the run.
              Flexible(
                child: Text(
                    _stopping ? 'Stopping…' : currentActivityLabel(s.events),
                    style: metaFor(context),
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
          child: Text(e.data, style: bodyFor(context)),
        ),
      );
    }
    if (e.baseKind == 'error') {
      return Container(
        margin: const EdgeInsets.symmetric(vertical: 10),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: const Color(0xFF2A1119),
          border: Border.all(color: const Color(0xFF6B2338)),
          borderRadius: BorderRadius.circular(T.rCard),
        ),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Icon(Icons.error_outline, size: 18, color: T.danger),
          const SizedBox(width: 10),
          Expanded(child: Text(e.data, style: bodyFor(context).copyWith(color: T.danger))),
        ]),
      );
    }
    if (e.baseKind == 'sources') return _sources(Source.listFrom(e.data));
    // The server's own note about the wait — a cold model loading, most often.
    // Dim and unbordered: it is the reason for a pause, not a message from
    // anybody, and it should not read as part of the answer.
    if (e.baseKind == 'meta') {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Icon(Icons.hourglass_empty, size: 15, color: T.textFaint),
          const SizedBox(width: 8),
          Expanded(child: Text(e.data, style: T.metaFaint)),
        ]),
      );
    }
    if (e.baseKind == 'done') {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 10),
        child: Row(children: [
          Icon(Icons.stop_circle_outlined, size: 15, color: T.textFaint),
          SizedBox(width: 8),
          Text('Stopped.', style: T.metaFaint),
        ]),
      );
    }

    // assistant text / review
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: ProseText(e.data),
    );
  }

  /// What the answer was built from, as cards you can move through.
  ///
  /// A vertical list of ten links pushes the answer off the screen and reads
  /// like a second search results page. Sideways, they sit under the answer as
  /// something to browse if you want to check, and cost one line of height if
  /// you do not.
  ///
  /// These are the pages the turn actually fetched, in the order it met them -
  /// not a list the model wrote, which is the only reason a tap target belongs
  /// on one.
  Widget _sources(List<Source> sources) {
    if (sources.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('SOURCES · ${sources.length}',
                style: T.metaFaint.copyWith(letterSpacing: 1.1, fontSize: 11)),
          ),
          SizedBox(
            height: 132,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              itemCount: sources.length,
              separatorBuilder: (_, __) => const SizedBox(width: 10),
              itemBuilder: (_, i) => _sourceCard(sources[i]),
            ),
          ),
        ],
      ),
    );
  }

  Widget _sourceCard(Source src) => SizedBox(
        width: 240,
        child: Material(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rCard),
          child: InkWell(
            borderRadius: BorderRadius.circular(T.rCard),
            onTap: () => launchUrl(Uri.parse(src.url),
                mode: LaunchMode.externalApplication),
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(src.label,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: bodyFor(context)
                          .copyWith(fontSize: 14, fontWeight: FontWeight.w600)),
                  if (src.snippet.isNotEmpty) ...[
                    const SizedBox(height: 4),
                    Expanded(
                      child: Text(src.snippet,
                          maxLines: 3,
                          overflow: TextOverflow.ellipsis,
                          style: T.metaFaint.copyWith(fontSize: 12.5)),
                    ),
                  ] else
                    const Spacer(),
                  const SizedBox(height: 6),
                  Row(children: [
                    // The site's own mark, fetched from the site. No third
                    // party is asked which pages this household reads.
                    _favicon(src),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(src.site,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: T.metaFaint.copyWith(fontSize: 12)),
                    ),
                    const Icon(Icons.north_east, size: 13, color: T.textFaint),
                  ]),
                ],
              ),
            ),
          ),
        ),
      );

  Widget _favicon(Source src) {
    if (src.site.isEmpty) {
      return const Icon(Icons.link, size: 14, color: T.textFaint);
    }
    return ClipRRect(
      borderRadius: BorderRadius.circular(4),
      child: Image.network(
        'https://${src.site}/favicon.ico',
        width: 14,
        height: 14,
        // Plenty of sites have no favicon at that path; a broken image icon
        // would be worse than none.
        errorBuilder: (_, __, ___) =>
            const Icon(Icons.public, size: 13, color: T.textFaint),
      ),
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
                        style: bodyFor(context),
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
        // Pressing Stop twice does nothing the first press did not already do.
        onTap: () => running
            ? (_stopping ? null : _stop(context))
            : _send(context),
        child: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: running && _stopping ? T.surfaceAlt : T.accent,
            shape: BoxShape.circle,
          ),
          child: running && _stopping
              ? const Padding(
                  padding: EdgeInsets.all(11),
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: T.textDim),
                )
              : Icon(running ? Icons.stop : Icons.arrow_upward,
                  size: 20, color: T.bg),
        ),
      );

  /// Ask the server to end the running turn, and say so while it unwinds.
  Future<void> _stop(BuildContext context) async {
    setState(() => _stopping = true);
    final err = await context.read<AppState>().interrupt();
    if (err == null || !context.mounted) return;
    // The request did not land, so the run is still going and the button has to
    // go back to being one that can be pressed.
    setState(() => _stopping = false);
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text('Could not stop: $err')));
  }

  Future<void> _send(BuildContext context) async {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    _input.clear();
    // Saying something is asking to see the answer, wherever you had scrolled
    // back to.
    _follow.pin();
    // Keep the text in the composer if it wasn't actually sent (e.g. the
    // keyboard-mash guard held it back), so a second tap sends the same thing.
    final sent = await context.read<AppState>().sendPrompt(text);
    if (!sent) _input.text = text;
  }
}

/// The events a chat renders, in reading order. Top-level so the ordering
/// rules are testable without building the widget.
///
/// Prompts, assistant prose, what it consulted, and anything that went wrong.
/// Tool and shell chrome stays hidden - but an error is not chrome, and "meta"
/// (the server explaining a wait, e.g. a cold model loading) is not either:
/// filtering them out is how a failure or a load looked like a hang. A
/// cancelled "done" is kept so that pressing Stop visibly stopped something.
///
/// Sources sink to the end of their turn. They are what the answer was built
/// from, which only makes sense under the answer — but the server emits them
/// when retrieval settles, which on a failed turn is before the error. Shown
/// in arrival order, the reading was "here are ten sources" followed by the
/// failure they never fed.
List<Event> chatMessages(List<Event> events) {
  final out = <Event>[];
  Event? pendingSources;
  void flushSources() {
    if (pendingSources != null) {
      out.add(pendingSources!);
      pendingSources = null;
    }
  }

  for (final e in events) {
    if (e.baseKind == 'sources') {
      pendingSources = e;
      continue;
    }
    // A new prompt starts a new turn; the previous turn's sources belong
    // above it, however that turn ended.
    if (e.baseKind == 'prompt') flushSources();
    if (const {'prompt', 'text', 'review', 'error', 'meta'}
            .contains(e.baseKind) ||
        (e.baseKind == 'done' && e.data == 'cancelled')) {
      out.add(e);
    }
  }
  flushSources();
  return out;
}
