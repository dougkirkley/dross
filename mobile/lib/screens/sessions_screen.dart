import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/app_drawer.dart';
import '../widgets/logo.dart';
import '../widgets/new_session_sheet.dart';
import '../widgets/prose.dart' show relTimeLong;
import 'chat_screen.dart';
import 'session_screen.dart';
import 'settings_screen.dart';

/// Session list attached to `dross serve`. The same server sessions back both
/// the Chat and Code tabs; `chat` only changes the header and which renderer a
/// tapped session opens (conversational vs full workspace). All model/provider
/// config lives on the server.
class SessionsScreen extends StatefulWidget {
  const SessionsScreen({super.key, this.chat = false});

  /// When true this is the Chat tab: stripped conversational view; otherwise the
  /// Code tab with the full workspace UI (tasks, shell, edits, uploads).
  final bool chat;

  @override
  State<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends State<SessionsScreen> {
  @override
  void initState() {
    super.initState();
    // Load sessions whenever the Code view mounts, so it populates on first
    // entry regardless of how the mode was switched.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final s = context.read<AppState>();
      if (s.remoteConfigured) s.refreshSessions();
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    return Scaffold(
      drawer: const AppDrawer(),
      body: SafeArea(
        child: Column(
          children: [
            FlatHeader(
              title: widget.chat ? 'Chat' : 'Code',
              leading: Builder(
                builder: (ctx) => CircleIconButton(
                    icon: Icons.menu,
                    onTap: () => Scaffold.of(ctx).openDrawer()),
              ),
            ),
            Expanded(
              child: !s.remoteConfigured
                  ? _needsConfig(context)
                  : RefreshIndicator(
                      color: T.accent,
                      backgroundColor: T.surface,
                      onRefresh: s.refreshSessions,
                      child: _body(context, s),
                    ),
            ),
          ],
        ),
      ),
      floatingActionButton: s.remoteConfigured
          ? PillButton(
              label: 'New session',
              icon: Icons.add,
              onTap: () => _create(context))
          : null,
    );
  }

  Widget _body(BuildContext context, AppState s) {
    final list = widget.chat ? s.chatSessions : s.codeSessions;
    return ListView(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 120),
        children: [
          const Text('Devices', style: T.meta),
          const SizedBox(height: 10),
          _card(
            child: const Center(
              child: Padding(
                padding: EdgeInsets.symmetric(vertical: 24),
                child: Text('No recently connected devices',
                    style: TextStyle(color: T.textDim)),
              ),
            ),
          ),
          const SizedBox(height: 28),
          Row(children: [
            const Text('Sessions', style: T.meta),
            const Spacer(),
            if (s.loadingSessions) const DrossSpinner(size: 18),
          ]),
          const SizedBox(height: 10),
          if (s.error != null && list.isEmpty)
            _card(
                child: Padding(
              padding: const EdgeInsets.all(16),
              child: Text('Error: ${s.error}',
                  style: const TextStyle(color: T.danger)),
            )),
          if (list.isEmpty && s.error == null)
            _card(
                child: const Padding(
              padding: EdgeInsets.all(20),
              child: Text('No sessions yet. Tap New session.',
                  style: TextStyle(color: T.textDim)),
            )),
          for (final sess in list) ...[
            _sessionCard(context, sess),
            const SizedBox(height: 14),
          ],
        ],
      );
  }

  Widget _sessionCard(BuildContext context, Session sess) => Dismissible(
        key: ValueKey(sess.id),
        direction: DismissDirection.endToStart,
        background: _swipeBg(),
        confirmDismiss: (_) => _confirmSheet(context, sess),
        onDismissed: (_) => context.read<AppState>().deleteSession(sess),
        child: _openTap(
          context,
          sess,
          _card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _statusIcon(sess.status),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(sess.displayTitle,
                                style: T.rowTitle,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis),
                            const SizedBox(height: 4),
                            _statusLine(sess),
                          ],
                        ),
                      ),
                      const SizedBox(width: 4),
                      Text(sess.updated != null ? relTimeLong(sess.updated!) : '',
                          style: T.metaFaint),
                      IconButton(
                        icon: const Icon(Icons.delete_outline,
                            size: 20, color: T.textDim),
                        tooltip: 'Delete session',
                        onPressed: () async {
                          if (await _confirmSheet(context, sess) &&
                              context.mounted) {
                            context.read<AppState>().deleteSession(sess);
                          }
                        },
                      ),
                    ],
                  ),
                if (sess.last.isNotEmpty) ...[
                  const SizedBox(height: 12),
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 12),
                    decoration: BoxDecoration(
                      color: T.bg,
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: Text(sess.last,
                        style: T.body,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );

  // Wraps a card so tapping it attaches to the session and opens it.
  Widget _openTap(BuildContext context, Session sess, Widget child) => InkWell(
        borderRadius: BorderRadius.circular(T.rCard),
        onTap: () {
          // Fire attach (non-blocking) and open immediately — the screen reacts
          // to state as events load, so re-entry never hangs on attach.
          final st = context.read<AppState>();
          st.attach(sess);
          st.markView(sess.id, widget.chat ? AppMode.chat : AppMode.code);
          Navigator.push(context, MaterialPageRoute(builder: (_) => _detail()));
        },
        child: child,
      );

  // The Chat tab opens the conversational ChatScreen; Code opens the full
  // workspace SessionScreen. Same backend session either way.
  Widget _detail() => widget.chat ? const ChatScreen() : const SessionScreen();

  // Red trailing background revealed while swiping a card to delete.
  Widget _swipeBg() => Container(
        alignment: Alignment.centerRight,
        padding: const EdgeInsets.only(right: 24),
        decoration: BoxDecoration(
          color: T.danger.withValues(alpha: 0.18),
          borderRadius: BorderRadius.circular(T.rCard),
        ),
        child: const Icon(Icons.delete_outline, color: T.danger),
      );

  // Status label (colored) + workspace. Device sessions show connection state
  // ("Connected · owner/repo"); pod sessions show run status.
  Widget _statusLine(Session sess) {
    final Color color;
    final String label;
    if (sess.device.isNotEmpty) {
      color = sess.connected ? T.ok : T.textFaint;
      label = sess.connected ? 'Connected' : 'Disconnected';
    } else {
      (color, label) = switch (sess.status) {
        'running' => (T.ok, 'running'),
        'error' => (T.danger, 'error'),
        _ => (T.textFaint, 'idle'),
      };
    }
    return Row(children: [
      if (sess.device.isNotEmpty) ...[
        Icon(Icons.laptop_mac,
            size: 13, color: sess.connected ? T.ok : T.textFaint),
        const SizedBox(width: 5),
      ],
      Flexible(
        child: Text(
          sess.workspace.isNotEmpty ? '$label · ${sess.workspace}' : label,
          style: TextStyle(color: color, fontSize: 13),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
    ]);
  }

  Widget _statusIcon(String status) {
    final (color, icon) = switch (status) {
      'running' => (T.accentHi, Icons.autorenew),
      'error' => (T.danger, Icons.error_outline),
      _ => (T.textFaint, Icons.circle_outlined),
    };
    return Container(
      width: 34,
      height: 34,
      decoration: BoxDecoration(
        color: T.bg,
        borderRadius: BorderRadius.circular(9),
        border: Border.all(color: T.border),
      ),
      child: Icon(icon, size: 17, color: color),
    );
  }

  Widget _card({required Widget child}) => Container(
        width: double.infinity,
        decoration: BoxDecoration(
          color: T.surface,
          borderRadius: BorderRadius.circular(T.rCard),
          border: Border.all(color: T.border),
        ),
        child: child,
      );

  Widget _needsConfig(BuildContext context) => Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(mainAxisSize: MainAxisSize.min, children: [
            const Text('Connect to dross serve', style: T.title),
            const SizedBox(height: 8),
            const Text('Remote mode attaches to your server for full, repo-backed sessions.',
                style: T.meta, textAlign: TextAlign.center),
            const SizedBox(height: 20),
            PillButton(
                label: 'Open Settings',
                icon: Icons.settings,
                onTap: () => _settings(context)),
          ]),
        ),
      );

  void _settings(BuildContext context) => Navigator.push(
      context, MaterialPageRoute(builder: (_) => const SettingsScreen()));

  /// Shows the delete-confirmation sheet and returns whether the user confirmed.
  /// It performs no mutation, so it is safe as a Dismissible confirmDismiss.
  Future<bool> _confirmSheet(BuildContext context, Session sess) async {
    final ok = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: T.surface,
      builder: (_) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(
            leading: const Icon(Icons.delete_outline, color: T.danger),
            title:
                const Text('Delete session', style: TextStyle(color: T.text)),
            subtitle: Text(sess.displayTitle, style: T.metaFaint),
            onTap: () => Navigator.pop(context, true),
          ),
        ]),
      ),
    );
    return ok == true;
  }

  Future<void> _create(BuildContext context) async {
    final state = context.read<AppState>();
    final choice = await showNewSessionSheet(context);
    if (choice == null || !context.mounted) return;
    final Session? s = await state.createSession(
        model: choice.model, workerModel: choice.worker);
    if (s != null && context.mounted) {
      state.attach(s); // non-blocking; screen reacts as events load
      state.markView(s.id, widget.chat ? AppMode.chat : AppMode.code);
      Navigator.push(context, MaterialPageRoute(builder: (_) => _detail()));
    }
  }
}
