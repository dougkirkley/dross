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

/// The Chat tab list: a flat "Chats" page of `dross serve` sessions, styled like
/// the Claude app's Chats page (title + rows only, no Devices/Sessions chrome).
/// Backed by the same sessions as Code; tapping opens the conversational
/// ChatScreen.
class ChatsScreen extends StatefulWidget {
  const ChatsScreen({super.key});
  @override
  State<ChatsScreen> createState() => _ChatsScreenState();
}

class _ChatsScreenState extends State<ChatsScreen> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final s = context.read<AppState>();
      if (s.remoteConfigured) s.refreshSessions();
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final chats = s.chatSessions;
    return Scaffold(
      drawer: const AppDrawer(),
      body: SafeArea(
        child: Column(
          children: [
            FlatHeader(
              title: 'Chats',
              leading: Builder(
                builder: (ctx) => CircleIconButton(
                    icon: Icons.menu,
                    onTap: () => Scaffold.of(ctx).openDrawer()),
              ),
            ),
            Expanded(
              child: chats.isEmpty
                  ? _empty()
                  : RefreshIndicator(
                      color: T.accent,
                      backgroundColor: T.surface,
                      onRefresh: s.refreshSessions,
                      child: ListView.separated(
                        padding: const EdgeInsets.fromLTRB(20, 8, 20, 120),
                        itemCount: chats.length,
                        separatorBuilder: (_, __) =>
                            const Divider(height: 1, color: T.borderFaint),
                        itemBuilder: (_, i) => _row(context, chats[i]),
                      ),
                    ),
            ),
          ],
        ),
      ),
      floatingActionButton: PillButton(
        label: 'New chat',
        icon: Icons.add,
        onTap: () => _create(context),
      ),
    );
  }

  Widget _row(BuildContext context, Session sess) => Dismissible(
        key: ValueKey(sess.id),
        direction: DismissDirection.endToStart,
        background: _swipeBg(),
        confirmDismiss: (_) => _confirmDelete(context, sess),
        onDismissed: (_) => context.read<AppState>().deleteSession(sess),
        child: InkWell(
          onTap: () => _open(context, sess),
          onLongPress: () async {
            if (await _confirmDelete(context, sess) && context.mounted) {
              await context.read<AppState>().deleteSession(sess);
            }
          },
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 16),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(sess.displayTitle,
                          style: T.rowTitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis),
                      const SizedBox(height: 4),
                      Text(sess.updated != null ? relTimeLong(sess.updated!) : '',
                          style: T.metaFaint),
                    ],
                  ),
                ),
                const Icon(Icons.chevron_right, color: T.textFaint),
              ],
            ),
          ),
        ),
      );

  void _open(BuildContext context, Session sess) {
    final st = context.read<AppState>();
    st.attach(sess);
    st.markView(sess.id, AppMode.chat);
    Navigator.push(
        context, MaterialPageRoute(builder: (_) => const ChatScreen()));
  }

  Future<void> _create(BuildContext context) async {
    final state = context.read<AppState>();
    final choice = await showNewSessionSheet(context);
    if (choice == null || !context.mounted) return;
    final Session? s = await state.createSession(
        model: choice.model, workerModel: choice.worker);
    if (s != null && context.mounted) {
      state.attach(s);
      state.markView(s.id, AppMode.chat);
      Navigator.push(
          context, MaterialPageRoute(builder: (_) => const ChatScreen()));
    }
  }

  Widget _empty() => Center(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(36, 0, 36, 80),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const DrossLogo(size: 132),
              const SizedBox(height: 28),
              Text('Cost-aware coding,\nin your pocket.',
                  style: T.prose.copyWith(
                      fontSize: 22, height: 1.35, color: T.textDim),
                  textAlign: TextAlign.center),
              const SizedBox(height: 14),
              const Text('Start a conversation with your agent.',
                  style: T.metaFaint, textAlign: TextAlign.center),
              const SizedBox(height: 24),
              const Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(Icons.south_east, size: 15, color: T.textFaint),
                SizedBox(width: 6),
                Text('Start a new chat', style: T.metaFaint),
              ]),
            ],
          ),
        ),
      );

  Widget _swipeBg() => Container(
        alignment: Alignment.centerRight,
        padding: const EdgeInsets.only(right: 24),
        decoration: BoxDecoration(
          color: T.danger.withValues(alpha: 0.18),
          borderRadius: BorderRadius.circular(T.rCard),
        ),
        child: const Icon(Icons.delete_outline, color: T.danger),
      );

  Future<bool> _confirmDelete(BuildContext context, Session sess) async {
    final ok = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: T.surface,
      builder: (_) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(
            leading: const Icon(Icons.delete_outline, color: T.danger),
            title: const Text('Delete chat', style: TextStyle(color: T.text)),
            subtitle: Text(sess.displayTitle, style: T.metaFaint),
            onTap: () => Navigator.pop(context, true),
          ),
        ]),
      ),
    );
    return ok == true;
  }
}
