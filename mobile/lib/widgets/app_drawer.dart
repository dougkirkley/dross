import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/logo.dart';
import '../screens/chat_screen.dart';
import '../screens/settings_screen.dart';

/// The left navigation drawer: a Chats tab (direct, in-browser chats) and a Code
/// tab (remote `ergon serve` sessions), plus recent chats. Mirrors the Claude
/// app's sidebar; the active tab is highlighted.
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
            _navItem(context, Icons.chat_bubble_outline, 'Chats',
                active: s.mode == ConnMode.direct,
                onTap: () => _go(context, ConnMode.direct)),
            _navItem(context, Icons.code, 'Code',
                active: s.mode == ConnMode.remote,
                onTap: () => _go(context, ConnMode.remote)),
            const SizedBox(height: 16),
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 0, 20, 8),
              child: Text('Recents', style: T.metaFaint),
            ),
            Expanded(
              child: s.chats.isEmpty
                  ? const Padding(
                      padding: EdgeInsets.symmetric(horizontal: 20),
                      child: Text('No chats yet', style: T.metaFaint),
                    )
                  : ListView.builder(
                      padding: EdgeInsets.zero,
                      itemCount: s.chats.length,
                      itemBuilder: (_, i) {
                        final c = s.chats[i];
                        return InkWell(
                          onTap: () {
                            final st = context.read<AppState>();
                            st.setMode(ConnMode.direct);
                            st.openChat(c);
                            Navigator.pop(context); // close drawer
                            Navigator.push(
                                context,
                                MaterialPageRoute(
                                    builder: (_) => const ChatScreen()));
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 20, vertical: 12),
                            child: Text(c.title,
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

  void _go(BuildContext context, ConnMode mode) {
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
