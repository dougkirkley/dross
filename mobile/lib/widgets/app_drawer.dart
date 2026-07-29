import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';
import '../screens/chat_screen.dart';
import '../screens/session_screen.dart';
import '../screens/settings_screen.dart';

/// The left navigation drawer: a Chat tab and a Code tab (both `ergon serve`
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
                  ErgonLogo(size: 34, showWordmark: false),
                  SizedBox(width: 12),
                  Text('Ergon', style: T.title),
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
                        // Each reopens in the view it was last used in.
                        return InkWell(
                          onTap: () {
                            final st = context.read<AppState>();
                            final view = st.viewOf(sess.id);
                            st.attach(sess);
                            Navigator.pop(context); // close drawer
                            Navigator.push(
                                context,
                                MaterialPageRoute(
                                    builder: (_) => view == AppMode.chat
                                        ? const ChatScreen()
                                        : const SessionScreen()));
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 20, vertical: 12),
                            child: Text(sess.displayTitle,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: T.body),
                          ),
                        );
                      },
                    ),
            ),
            const Divider(height: 1, color: T.borderFaint),
            _navItem(context, Icons.settings_outlined, 'Settings',
                active: false, onTap: () {
              Navigator.pop(context);
              Navigator.push(context,
                  MaterialPageRoute(builder: (_) => const SettingsScreen()));
            }),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  void _go(BuildContext context, AppMode mode) {
    Navigator.pop(context); // close drawer
    context.read<AppState>().setMode(mode); // Home re-routes to the tab
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
