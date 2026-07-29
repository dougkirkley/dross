import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';
import '../screens/settings_screen.dart';

/// The left navigation drawer: the app is a thin `ergon serve` client, so it
/// holds the identity header, a link to Settings (server URL), and sign out.
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
              padding: EdgeInsets.fromLTRB(20, 16, 20, 4),
              child: Row(
                children: [
                  ErgonLogo(size: 34, showWordmark: false),
                  SizedBox(width: 12),
                  Text('Ergon', style: T.title),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Text(s.baseUrl,
                  maxLines: 1, overflow: TextOverflow.ellipsis, style: T.metaFaint),
            ),
            const Divider(height: 1, color: T.borderFaint),
            const Spacer(),
            const Divider(height: 1, color: T.borderFaint),
            _navItem(context, Icons.settings_outlined, 'Settings', onTap: () {
              Navigator.pop(context);
              Navigator.push(context,
                  MaterialPageRoute(builder: (_) => const SettingsScreen()));
            }),
            _navItem(context, Icons.logout, 'Sign out', onTap: () {
              Navigator.pop(context);
              context.read<AppState>().signOut();
            }),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  Widget _navItem(BuildContext context, IconData icon, String label,
          {required VoidCallback onTap}) =>
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 2),
        child: Material(
          color: Colors.transparent,
          borderRadius: BorderRadius.circular(12),
          child: InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: onTap,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
              child: Row(children: [
                Icon(icon, size: 22, color: T.textDim),
                const SizedBox(width: 16),
                Text(label,
                    style: const TextStyle(
                        color: T.textDim, fontSize: 17, fontWeight: FontWeight.w500)),
              ]),
            ),
          ),
        ),
      );
}
