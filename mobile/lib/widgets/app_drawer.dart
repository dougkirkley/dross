import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';
import '../screens/chat_screen.dart';
import '../screens/session_screen.dart';
import '../screens/settings_screen.dart';
import '../screens/admin_screen.dart';
import '../util/nav.dart';
import 'row_actions.dart';
import 'session_actions.dart';

/// The left navigation drawer: a Chat tab and a Code tab (both `dross serve`
/// sessions, differing only in presentation), plus recent sessions. Mirrors the
/// Claude app's sidebar; the active tab is highlighted.
class AppDrawer extends StatelessWidget {
  const AppDrawer({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    return Drawer(
      backgroundColor: T.bg,
      shape: const RoundedRectangleBorder(),
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 16, 20, 20),
              child: Row(
                children: [
                  DrossLogo(size: 34, showWordmark: false),
                  SizedBox(width: 12),
                  Text('Dross', style: T.title),
                ],
              ),
            ),
            _navItem(context, Icons.chat_bubble_outline, 'Chat',
                active: s.mode == AppMode.chat,
                onTap: () => _go(context, AppMode.chat)),
            _navItem(context, Icons.code, 'Code',
                active: s.mode == AppMode.code,
                onTap: () => _go(context, AppMode.code)),
            const SizedBox(height: 16),
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 0, 20, 8),
              child: Text('Recents', style: T.metaFaint),
            ),
            Expanded(
              child: s.sessions.isEmpty
                  ? const Padding(
                      padding: EdgeInsets.symmetric(horizontal: 20),
                      child: Text('No sessions yet', style: T.metaFaint),
                    )
                  : ListView.builder(
                      padding: EdgeInsets.zero,
                      itemCount: s.sessions.length,
                      itemBuilder: (_, i) {
                        final sess = s.sessions[i];
                        // Combined recents: chats and code in one flat list.
                        // Each reopens in the view it was last used in, with an
                        // icon marking which kind it is.
                        final view = s.viewOf(sess.id);
                        final isChat = view == AppMode.chat;
                        // Rename and delete are reachable from here too. This
                        // is the only session list a desktop window shows
                        // without navigating away from the conversation, so
                        // leaving it action-less meant going to the list screen
                        // to rename the thing already open beside it.
                        return RowActions(
                          key: ValueKey(sess.id),
                          actions: sessionActions(context, sess),
                          child: Material(
                            color: T.bg,
                            child: InkWell(
                              onTap: () {
                                final st = context.read<AppState>();
                                st.attach(sess);
                                _closeDrawer(context);
                                openConversation(
                                    context,
                                    isChat
                                        ? const ChatScreen()
                                        : const SessionScreen());
                              },
                              child: Padding(
                                padding: const EdgeInsets.symmetric(
                                    horizontal: 20, vertical: 12),
                                child: Row(
                                  children: [
                                    Icon(
                                        isChat
                                            ? Icons.chat_bubble_outline
                                            : Icons.code,
                                        size: 16,
                                        color: T.textDim),
                                    const SizedBox(width: 10),
                                    Expanded(
                                      child: Text(sess.displayTitle,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: T.body),
                                    ),
                                    // Room for the menu button, so a long title
                                    // does not run underneath it.
                                    const SizedBox(width: 24),
                                  ],
                                ),
                              ),
                            ),
                          ),
                        );
                      },
                    ),
            ),
            const Divider(height: 1, color: T.borderFaint),
            // Only for an admin, and only because the server said so. Hiding it
            // is courtesy, not the permission: the API refuses anyone else.
            //
            // Not a cog: Settings already owns that glyph, and two cogs in one
            // drawer is a coin toss every time somebody wants one of them.
            if (s.isAdmin)
              _navItem(context, Icons.admin_panel_settings_outlined, 'Admin',
                  active: false, onTap: () {
                _closeDrawer(context);
                Navigator.push(context,
                    MaterialPageRoute(builder: (_) => const AdminScreen()));
              }),
            _navItem(context, Icons.settings_outlined, 'Settings',
                active: false, onTap: () {
              _closeDrawer(context);
              Navigator.push(context,
                  MaterialPageRoute(builder: (_) => const SettingsScreen()));
            }),
            _navItem(context, Icons.logout, 'Sign out', active: false,
                onTap: () {
              _closeDrawer(context);
              context.read<AppState>().signOut();
            }),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  void _go(BuildContext context, AppMode mode) {
    _closeDrawer(context);
    context.read<AppState>().setMode(mode); // Home re-routes to the tab
    // Home is what shows the tab, and it lives at the bottom of the stack. On
    // a phone the drawer only exists on Home, so this is a no-op; on desktop
    // the sidebar rides on every screen, and without this the tap changed a
    // mode nobody could see under the conversation still on top.
    Navigator.popUntil(context, (r) => r.isFirst);
  }

  /// Closes the drawer — when there is one. On wide screens this widget is a
  /// permanent sidebar, not an overlay, and the unconditional pop that used to
  /// live at every tap site was popping the Home route instead: whatever got
  /// pushed next sat on an empty stack, its back button led to a white screen,
  /// and the stock AppBar's implied back never appeared because there was
  /// nothing to pop to.
  static void _closeDrawer(BuildContext context) {
    final scaffold = Scaffold.maybeOf(context);
    if (scaffold != null && scaffold.isDrawerOpen) {
      Navigator.pop(context);
    }
  }

  Widget _navItem(BuildContext context, IconData icon, String label,
          {required bool active, required VoidCallback onTap}) =>
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 2),
        child: Material(
          color: active ? T.surface : Colors.transparent,
          borderRadius: BorderRadius.circular(12),
          child: InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: onTap,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
              child: Row(children: [
                Icon(icon, size: 22, color: active ? T.accentHi : T.textDim),
                const SizedBox(width: 16),
                Text(label,
                    style: TextStyle(
                        color: active ? T.text : T.textDim,
                        fontSize: 17,
                        fontWeight: FontWeight.w500)),
              ]),
            ),
          ),
        ),
      );
}
