import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/readable.dart';
import '../widgets/app_drawer.dart';
import '../widgets/logo.dart';
import '../widgets/prose.dart' show relTimeLong;
import 'chat_screen.dart';
import '../widgets/row_actions.dart';
import '../widgets/session_actions.dart';

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
    final wide = isWide(context);
    return Scaffold(
      // At desktop widths the sidebar is simply there, beside the content. A
      // drawer is a way to borrow space a phone does not have; on a wide window
      // hiding it behind a button costs a click to reach settings that would
      // otherwise always be in view.
      drawer: wide ? null : const AppDrawer(),
      body: SafeArea(
        child: Row(children: [
          if (wide) const SizedBox(width: 300, child: AppDrawer()),
          if (wide) const VerticalDivider(width: 1, color: T.border),
          Expanded(
              child: Column(
            children: [
              FlatHeader(
                title: 'Chats',
                // No hamburger when the sidebar is already open.
                leading: wide
                    ? null
                    : Builder(
                        builder: (ctx) => CircleIconButton(
                            icon: Icons.menu,
                            onTap: () => Scaffold.of(ctx).openDrawer()),
                      ),
              ),
              Expanded(
                child: !s.sessionsLoaded
                    ? const Center(child: DrossSpinner(size: 40))
                    : RefreshIndicator(
                        color: T.accent,
                        backgroundColor: T.surface,
                        onRefresh: s.refreshApp,
                        child: chats.isEmpty
                            // The empty state is exactly when a refresh is wanted, so
                            // give it scroll physics rather than leaving it inert.
                            ? ListView(
                                physics: const AlwaysScrollableScrollPhysics(),
                                children: [
                                  SizedBox(
                                      height:
                                          MediaQuery.of(context).size.height *
                                              0.65,
                                      child: _empty()),
                                ],
                              )
                            : ListView.separated(
                                physics: const AlwaysScrollableScrollPhysics(),
                                padding:
                                    const EdgeInsets.fromLTRB(20, 8, 20, 120),
                                itemCount: chats.length,
                                separatorBuilder: (_, __) => const Divider(
                                    height: 1, color: T.borderFaint),
                                itemBuilder: (_, i) => _row(context, chats[i]),
                              ),
                      ),
              ),
            ],
          )),
        ]),
      ),
      floatingActionButton: PillButton(
        label: 'New chat',
        icon: Icons.add,
        onTap: () => _create(context),
      ),
    );
  }

  // Swiping used to dismiss the row outright, with a sheet to confirm it, and
  // a long-press for the same thing. Both are gone: the swipe reveals what can
  // be done instead of doing it, which is what makes room for renaming a chat
  // from the list rather than having to open it first — and it means the
  // confirmation is a tap on a small red target rather than a sheet thrown up
  // to ask about something the gesture already said.
  //
  // A pointer gets the same two actions from a menu on the row instead; see
  // RowActions for why the gesture is not offered to it.
  Widget _row(BuildContext context, Session sess) => RowActions(
        key: ValueKey(sess.id),
        actions: sessionActions(context, sess),
        // Opaque, so the row travelling left covers the buttons rather than
        // showing them through it. The list rows have no card of their own.
        child: Material(
          color: T.bg,
          child: InkWell(
            onTap: () => _open(context, sess),
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
                        Text(
                            sess.updated != null
                                ? relTimeLong(sess.updated!)
                                : '',
                            style: T.metaFaint),
                      ],
                    ),
                  ),
                  const Icon(Icons.chevron_right, color: T.textFaint),
                ],
              ),
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
    // Chat session: default model, and no file/shell tools server-side.
    final Session? s = await state.createSession(chat: true);
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
                  style: T.prose
                      .copyWith(fontSize: 22, height: 1.35, color: T.textDim),
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
}
