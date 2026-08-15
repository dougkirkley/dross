import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/ask_card.dart';
import '../widgets/readable.dart';
import '../util/file_pick.dart';
import '../widgets/activity.dart';
import '../widgets/follow_bottom.dart';
import '../widgets/rename_dialog.dart';
import '../widgets/prose.dart';
import '../widgets/logo.dart';
import '../widgets/server_model_sheet.dart';
import '../widgets/tasks_sheet.dart';
import '../util/nav.dart';
import '../widgets/app_drawer.dart';
import '../widgets/composer_keys.dart';
import '../widgets/changed_files.dart';
import '../widgets/speaker.dart';

/// Remote-session conversation (Code view). Streams the serve event log (tool
/// calls, delegation indented, cost). Prose is set in a reading face — it used
/// to be mono end to end, to match the TUI rendering the same session, which
/// made long output harder to read than it had any need to be. Code, diffs and
/// tool output keep mono, where alignment matters. The Chat view uses
/// ChatScreen.
class SessionScreen extends StatefulWidget {
  const SessionScreen({super.key});
  @override
  State<SessionScreen> createState() => _SessionScreenState();
}

class _SessionScreenState extends State<SessionScreen> {
  final _input = TextEditingController();
  final _inputFocus = FocusNode();
  final _follow = FollowBottom();

  AppState? _app;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // Held so dispose can reach it: by then this element is off the tree and
    // its context can no longer look anything up.
    _app = context.read<AppState>();
  }

  @override
  void dispose() {
    // Read on the way out. The reply usually lands while the session is open,
    // after it was marked read on entry, so without this a session you just
    // finished reading shows as unread the moment you leave it.
    final s = _app?.current;
    if (s != null) _app?.markRead(s);
    _input.dispose();
    _inputFocus.dispose();
    _follow.dispose();
    super.dispose();
  }

  // Move [text] into the composer: set it with the cursor at the end and focus
  // the field. Setting controller.text alone leaves the selection invalid and
  // the field unfocused, which on web reads as "nothing happened".
  void _moveToInput(String text) {
    if (text.isEmpty) return;
    _input.value = TextEditingValue(
      text: text,
      selection: TextSelection.collapsed(offset: text.length),
    );
    _inputFocus.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final sess = s.current;
    _follow.follow();
    // Sidebar beside the transcript on desktop widths, same as ChatScreen and
    // for the same reason: a full-bleed page with a phone back button is what
    // made the web build read as a stretched phone.
    final wide = isWide(context);
    final pane = Column(
      children: [
        FlatHeader(
          // The session's own name when it has one, so a renamed session is
          // identifiable from the screen it is open on.
          title: sess?.displayTitle ?? 'session',
          subtitle: '\$${(sess?.cost ?? 0).toStringAsFixed(4)}',
          leading: wide
              ? null
              : CircleIconButton(
                  icon: Icons.chevron_left, onTap: () => backOrHome(context)),
          trailing: sess == null
              ? null
              : _sessionMenu(context, sess, changedFiles(s.events)),
        ),
        // Same signal at the top of the window the session owns, so a run
        // is visible without going back to the list to see its card.
        if (sess?.isRunning ?? false) const RunningBar(),
        if (_starting(sess))
          // In the same column the transcript reads in. Left to itself it
          // stretched the full width of a desktop window while every line
          // under it sat in a narrower one, so the two did not look like the
          // same screen.
          Readable(
            child: _StartingPanel(
              resuming: isResuming(s.events),
              setup: sess?.setup ?? '',
            ),
          ),
        Expanded(
          child: Builder(builder: (_) {
            // The panel above is already saying what the session is setting
            // up. Printing the same sentence directly underneath it said it
            // twice and made one of them look like it was about something
            // else. Once setup is done the panel goes and the line stays, so
            // the transcript still holds the record.
            final timeline = buildTimeline(
              withoutLiveSetup(s.events, sess?.setup ?? ''),
              running: sess?.isRunning ?? false,
            );
            final shells = buildShells(s.events);
            final runningShells = shells.where((x) => x.running).length;
            // Trailing items after the conversation: the live spinner (while
            // the turn runs) and a "N running task" link (background shells).
            final trailing = <Widget>[
              if (sess?.isRunning ?? false) _runningRow(s),
              if (runningShells > 0) _tasksLink(context, shells, runningShells),
            ];
            return RefreshIndicator(
              onRefresh: () => context.read<AppState>().reloadCurrent(),
              color: T.accent,
              backgroundColor: T.surface,
              child: Readable(
                child: ListView.builder(
                  controller: _follow.controller,
                  padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
                  itemCount: timeline.length + trailing.length,
                  itemBuilder: (_, j) {
                    if (j >= timeline.length) {
                      return trailing[j - timeline.length];
                    }
                    final item = timeline[j];
                    if (item is Activity) return ActivityBlock(item);
                    return _eventRow(item as Event);
                  },
                ),
              ),
            );
          }),
        ),
        AnimatedBuilder(
          animation: _follow,
          builder: (_, __) => JumpToLatest(
              follow: _follow,
              label: (sess?.isRunning ?? false) ? 'Working' : 'Latest'),
        ),
        if (s.queue.isNotEmpty) Readable(child: _queueStrip(context, s)),
        Readable(child: _inputBar(context, s)),
      ],
    );
    return SelectionArea(
        child: Scaffold(
      backgroundColor: T.chatBg,
      body: SafeArea(
        child: wide
            ? Row(children: [
                const SizedBox(width: 300, child: AppDrawer()),
                Container(width: 1, color: T.border),
                Expanded(child: pane),
              ])
            : pane,
      ),
    ));
  }

  /// The session's own menu, top right of the page it belongs to.
  ///
  /// Changed files live here now. They were a chip pinned above the
  /// transcript, then the transcript's first row — and a first row does not
  /// scroll away on a short session, which is most of them, so it read as
  /// something stuck to the screen. It is a lookup you want occasionally, and
  /// this is where occasional lookups belong. The count is still one tap away,
  /// and the sheet still totals it.
  Widget _sessionMenu(BuildContext context, Session sess, List<ChangedFile> changed) =>
      PopupMenuButton<String>(
        icon: const Icon(Icons.more_horiz, color: T.text),
        color: T.surface,
        position: PopupMenuPosition.under,
        onSelected: (v) {
          switch (v) {
            case 'rename':
              showRenameDialog(context, sess);
            case 'changed':
              showChangedFilesSheet(context, changed);
          }
        },
        itemBuilder: (_) => [
          const PopupMenuItem(
            value: 'rename',
            child: Row(children: [
              Icon(Icons.drive_file_rename_outline, size: 18, color: T.textDim),
              SizedBox(width: 10),
              Text('Rename', style: T.body),
            ]),
          ),
          if (changed.isNotEmpty)
            PopupMenuItem(
              value: 'changed',
              child: Row(children: [
                const Icon(Icons.difference_outlined,
                    size: 18, color: T.textDim),
                const SizedBox(width: 10),
                Text('Changed files (${changed.length})', style: T.body),
              ]),
            ),
        ],
      );

  Widget _tasksLink(
          BuildContext context, List<ShellTask> shells, int running) =>
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => showBackgroundTasksSheet(context, shells),
          child: Row(mainAxisSize: MainAxisSize.min, children: [
            const Text('✻ ', style: TextStyle(color: T.accentHi, fontSize: 15)),
            Text('$running running task${running == 1 ? '' : 's'}',
                style: const TextStyle(
                    color: T.accentHi,
                    fontSize: 15,
                    fontWeight: FontWeight.w600)),
          ]),
        ),
      );

  Widget _runningRow(AppState s) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(children: [
          const DrossSpinner(size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(currentActivityLabel(s.events),
                style: metaFor(context),
                maxLines: 1,
                overflow: TextOverflow.ellipsis),
          ),
        ]),
      );

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
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => _moveToInput(
                          context.read<AppState>().removeQueued(i)),
                      child: Text(s.queue[i],
                          style: metaFor(context),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis),
                    ),
                  ),
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: Icon(Icons.arrow_upward,
                        size: 18, color: s.canPromote(i) ? T.accent : T.border),
                    tooltip: s.canPromote(i) ? 'Run next' : 'Already next',
                    // Null rather than a handler that returns immediately: the
                    // front of the queue is already next, and a live-looking
                    // arrow that swallows the tap is what reads as broken.
                    onPressed: s.canPromote(i)
                        ? () => context.read<AppState>().promoteRemote(i)
                        : null,
                  ),
                  IconButton(
                    visualDensity: VisualDensity.compact,
                    icon: const Icon(Icons.edit_outlined,
                        size: 18, color: T.textDim),
                    tooltip: 'Edit',
                    onPressed: () =>
                        _moveToInput(context.read<AppState>().removeQueued(i)),
                  ),
                ]),
              ),
          ],
        ),
      );

  Widget _eventRow(Event e) {
    final indent =
        e.isWorker ? const EdgeInsets.only(left: 20) : EdgeInsets.zero;
    Widget content;
    switch (e.baseKind) {
      case 'ask-request':
        // Blocking the run, so it gets its own card rather than a text row.
        return AskCard(e.data);
      case 'prompt':
        // One layout at every width: a rule down the left in the speaker's
        // colour, the mark the terminal already draws. This used to be a bubble
        // at the right margin on a phone and a "▸ you" prefix on a desktop —
        // two answers to one question, which made the same session read as two
        // different products in two windows.
        //
        // The tint is what the bubble was really for, kept: on a phone, "this
        // one is mine" wants more than two pixels of colour.
        content = SpeakerTurn(
          speaker: Speaker.you,
          tinted: true,
          child: Text(e.data, style: bodyFor(context)),
        );
        break;
      case 'text':
        // The rule says whose turn this is, and that is enough. The orb that
        // used to lead it said the same thing a second time, once per block —
        // and a run that writes a line before each tool call turns that into a
        // column of markers down a screen of two-line paragraphs.
        content = SpeakerTurn(
          speaker: Speaker.agent,
          child: ProseText(e.data),
        );
        break;
      case 'review':
        content = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('code review', style: T.metaFaint),
            const SizedBox(height: 2),
            ProseText(e.data),
          ],
        );
        break;
      case 'tool':
        content = _mono('→ ${e.data}', T.accentHi);
        break;
      case 'tool-result':
        content = _mono(e.data, T.textFaint, max: 3);
        break;
      case 'checkpoint':
        content = _mono('✓ ${e.data}', T.accentHi);
        break;
      case 'meta':
        // The session saying what it is doing before it can do anything —
        // cloning a repository, waiting on a model to load. Undecorated: this
        // is git's own output, near enough, and a bullet in front of it would
        // only make it look like something else.
        content = _mono(e.data, T.textFaint);
        break;
      case 'error':
        content = _ErrorRow(e.data);
        break;
      case 'done':
        // A run stopped by the user closes as "cancelled" — a normal ending,
        // not "done · <cost>" and not an error.
        content = e.data == 'cancelled'
            ? _mono('⊘ cancelled', T.textFaint)
            : _mono('done · ${e.data}', T.textFaint);
        break;
      default:
        content = _mono(e.data, T.textDim);
    }
    return Padding(
        padding: const EdgeInsets.symmetric(vertical: 6).add(indent),
        child: content);
  }

  Widget _mono(String text, Color color, {int? max}) => Text(
        text,
        maxLines: max,
        overflow: max != null ? TextOverflow.ellipsis : TextOverflow.clip,
        style: T.mono.copyWith(color: color),
      );

  Widget _inputBar(BuildContext context, AppState s) {
    final running = s.current?.isRunning ?? false;
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
        // Enter sends where there is a keyboard to press it with. A running
        // turn still accepts one: this composer queues while the agent works.
        child: SendOnEnter(
          onSend: () => _send(context),
          child: Container(
            padding: const EdgeInsets.fromLTRB(14, 6, 8, 6),
            decoration: BoxDecoration(
              color: T.inputBg,
              borderRadius: BorderRadius.circular(T.rCard),
              border: Border.all(color: T.border),
            ),
            child: Column(
              children: [
                if (s.mashWarning != null)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 6, top: 2),
                    child: Row(children: [
                      const Icon(Icons.pets, size: 15, color: T.textDim),
                      const SizedBox(width: 8),
                      Expanded(child: Text(s.mashWarning!, style: T.metaFaint)),
                    ]),
                  ),
                if (s.remotePending.isNotEmpty) _attachChips(context, s),
                Row(children: [
                  // The terminal's prompt marker, on the desktop layout only. On
                  // a phone the composer is obviously a composer and the glyph
                  // would just take a character's width from a short line.
                  if (isWide(context))
                    Padding(
                      padding: const EdgeInsets.only(right: 8),
                      child: Text('❯',
                          style: T.termBody.copyWith(
                              color: T.accent, fontWeight: FontWeight.w600)),
                    ),
                  Expanded(
                    child: TextField(
                      controller: _input,
                      focusNode: _inputFocus,
                      minLines: 1,
                      maxLines: 5,
                      style: bodyFor(context),
                      decoration: InputDecoration.collapsed(
                          hintText: running
                              ? 'Queue after this turn…'
                              : 'Build anything…',
                          hintStyle: const TextStyle(color: T.textDim)),
                      onSubmitted: (_) => _send(context),
                    ),
                  ),
                  // Button is a square by default; it becomes the round send arrow
                  // only when the composer has text. While running with an empty
                  // composer the square acts as the Stop control (interrupt).
                  ValueListenableBuilder<TextEditingValue>(
                    valueListenable: _input,
                    builder: (context, value, _) {
                      final hasText = value.text.trim().isNotEmpty;
                      if (hasText) {
                        return GestureDetector(
                          onTap: () => _send(context),
                          child: Container(
                            width: 40,
                            height: 40,
                            decoration: BoxDecoration(
                              gradient: T.auroraGrad,
                              shape: BoxShape.circle,
                              boxShadow: [
                                BoxShadow(
                                  color: T.violet.withValues(alpha: .5),
                                  blurRadius: 16,
                                  spreadRadius: -2,
                                ),
                              ],
                            ),
                            child: Icon(
                                running ? Icons.add : Icons.arrow_upward,
                                size: 20,
                                color: T.bg),
                          ),
                        );
                      }
                      // Running with an empty composer: the square is Stop.
                      if (running) {
                        return GestureDetector(
                          onTap: () => context.read<AppState>().interrupt(),
                          child: Container(
                            width: 40,
                            height: 40,
                            decoration: BoxDecoration(
                              color: T.accent,
                              borderRadius: BorderRadius.circular(10),
                            ),
                            child:
                                const Icon(Icons.stop, size: 18, color: T.bg),
                          ),
                        );
                      }
                      // Idle with nothing typed: a dimmed, inert send arrow — the
                      // resting state shouldn't offer a Stop for a run that isn't
                      // happening.
                      return Container(
                        width: 40,
                        height: 40,
                        decoration: const BoxDecoration(
                          color: T.surfaceAlt,
                          shape: BoxShape.circle,
                        ),
                        child: const Icon(Icons.arrow_upward,
                            size: 20, color: T.textFaint),
                      );
                    },
                  ),
                ]),
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
                  if (isWide(context))
                    Text('Enter to send · Shift+Enter for a new line',
                        style: T.metaFaint.copyWith(fontSize: 11.5)),
                ]),
              ],
            ),
          ),
        ),
      ),
    );
  }

  /// A pod-backed session that has not linked yet. Its workload is still being
  /// scheduled, pulling an image or attaching a volume, and the server has
  /// nothing to relay a prompt to.
  static bool _starting(Session? sess) =>
      sess != null && sess.device.isNotEmpty && !sess.connected;

  Future<void> _send(BuildContext context) async {
    final text = _input.text;
    if (text.trim().isEmpty) return;
    if (_starting(context.read<AppState>().current)) {
      // The server would refuse this with a conflict and the app would drop it
      // silently, which reads as a dead send button.
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text('Session is still starting — try again in a moment.'),
      ));
      return;
    }
    _input.clear();
    // Saying something is asking to see the answer, wherever you had scrolled
    // back to.
    _follow.pin();
    // Keep the text in the composer if it wasn't actually sent (e.g. the
    // keyboard-mash guard held it back), so a second tap sends the same thing.
    final sent = await context.read<AppState>().sendPrompt(text);
    if (!sent) _moveToInput(text);
  }

  Widget _circleAction(IconData icon, VoidCallback onTap) => GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          width: 36,
          height: 36,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: T.border),
          ),
          child: Icon(icon, size: 18, color: T.text),
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

  Widget _attachChips(BuildContext context, AppState s) => Padding(
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
}

/// Shown while a pod-backed session has no workload attached.
///
/// Two ways to arrive here and they deserve different words. A new session is
/// waiting on a workload being scheduled for the first time. One with a
/// conversation behind it has had its workload taken away — restarted, or
/// replaced to move it onto a new version — and what the reader wants to know
/// is whether the work is gone. It is not: the workspace is on a volume that
/// outlives the pod.
///
/// Which of the two is inferred from whether the session has said anything
/// yet, not reported by the operator doing the replacing.
/// Whether a workspace coming up is coming back rather than arriving.
///
/// Inferred from whether the session has said anything yet, because the
/// operator replacing a pod does not report which it is doing.
///
/// Setup lines do not count. A session started on a repository says it is
/// cloning before its pod has ever run, and taking that as "has said
/// something" made every first start read as a restart — telling somebody
/// their files were being brought back when there were not yet any files.
bool isResuming(List<Event> events) => events.any((e) => e.baseKind != 'meta');

class _StartingPanel extends StatelessWidget {
  const _StartingPanel({required this.resuming, this.setup = ''});
  final bool resuming;

  /// What the session is waiting on, when it knows. It replaces the generic
  /// line rather than joining it: "waiting for its workspace to come up" is
  /// what you say when you cannot say anything better.
  final String setup;

  @override
  Widget build(BuildContext context) => Container(
        width: double.infinity,
        margin: const EdgeInsets.fromLTRB(16, 8, 16, 0),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rCard),
          border: Border.all(color: T.border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const RunningBar(),
            const SizedBox(height: 10),
            Text(resuming ? 'Session is restarting…' : 'Starting session…',
                style: T.body),
            const SizedBox(height: 4),
            Text(
              setup.isNotEmpty
                  ? setup
                  : resuming
                      ? 'Its workspace is being brought back. Files are kept; a '
                          'reply that was in progress is not.'
                      : 'Waiting for its workspace to come up.',
              style: T.metaFaint,
            ),
          ],
        ),
      );
}

/// Strips the fences a quoted region arrives wrapped in.
///
/// They are for the model: they keep leading whitespace intact through its own
/// formatting. Nothing renders markdown in this row, so they arrive as two
/// stray lines of backticks around the quote.
String _unfenced(String s) => s
    .split('\n')
    .where((l) => l.trimRight() != '```')
    .join('\n')
    .trim();

/// A tool error: one red line, the rest behind a triangle.
///
/// These messages carry their evidence with them — a failed edit quotes the
/// region it could not match — so printing them whole put a dozen lines of file
/// in the middle of the conversation for something the run usually recovered
/// from on its next call. What went wrong is one line. Whether you want to see
/// why is a question, and a question deserves a control rather than an answer
/// nobody asked for.
///
/// Not auto-opened. The activity block opens itself on a failure because a
/// failed command is often where a run ends; a failed edit is a retry, and this
/// is the one that repeats.
class _ErrorRow extends StatefulWidget {
  const _ErrorRow(this.data);
  final String data;

  @override
  State<_ErrorRow> createState() => _ErrorRowState();
}

class _ErrorRowState extends State<_ErrorRow> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    final cut = widget.data.indexOf('\n');
    final head = (cut < 0 ? widget.data : widget.data.substring(0, cut)).trim();
    final rest = cut < 0 ? '' : _unfenced(widget.data.substring(cut + 1));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          onTap: rest.isEmpty ? null : () => setState(() => _open = !_open),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Padding(
                padding: EdgeInsets.only(top: 2),
                child: Icon(Icons.warning_amber_rounded,
                    size: 16, color: T.danger),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(head,
                    style: T.mono.copyWith(color: T.danger),
                    maxLines: _open ? null : 3,
                    overflow:
                        _open ? TextOverflow.clip : TextOverflow.ellipsis),
              ),
              if (rest.isNotEmpty)
                Icon(_open ? Icons.expand_more : Icons.chevron_right,
                    size: 18, color: T.textFaint),
            ],
          ),
        ),
        if (_open && rest.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(left: 24, top: 6),
            child: Text(rest, style: T.mono.copyWith(color: T.textFaint)),
          ),
      ],
    );
  }
}
