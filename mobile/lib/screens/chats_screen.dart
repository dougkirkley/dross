import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/app_drawer.dart';
import '../widgets/logo.dart';
import 'chat_screen.dart';
import 'settings_screen.dart';

/// Direct-mode conversation list. Flat rows (title + relative time + chevron),
/// a near-white "New chat" pill, and a search field — matching the Claude app's
/// Chats page rather than a Material list.
class ChatsScreen extends StatefulWidget {
  const ChatsScreen({super.key});
  @override
  State<ChatsScreen> createState() => _ChatsScreenState();
}

class _ChatsScreenState extends State<ChatsScreen> {
  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final chats = s.chats;
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
              trailing: CircleIconButton(
                  icon: Icons.tune, onTap: () => _openSettings(context)),
            ),
            Expanded(
              child: chats.isEmpty
                  ? _empty()
                  : ListView.separated(
                      padding: const EdgeInsets.fromLTRB(20, 8, 20, 120),
                      itemCount: chats.length,
                      separatorBuilder: (_, __) =>
                          const Divider(height: 1, color: T.borderFaint),
                      itemBuilder: (_, i) => _row(context, chats[i]),
                    ),
            ),
          ],
        ),
      ),
      floatingActionButton: PillButton(
        label: 'New chat',
        icon: Icons.add,
        onTap: () {
          context.read<AppState>().newChat();
          Navigator.push(context,
              MaterialPageRoute(builder: (_) => const ChatScreen()));
        },
      ),
    );
  }

  Widget _row(BuildContext context, DirectChat c) => InkWell(
        onTap: () {
          context.read<AppState>().openChat(c);
          Navigator.push(context,
              MaterialPageRoute(builder: (_) => const ChatScreen()));
        },
        onLongPress: () => _confirmDelete(context, c),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 16),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(c.title,
                        style: T.rowTitle,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                    const SizedBox(height: 4),
                    Text(relTimeLong(c.updated), style: T.metaFaint),
                  ],
                ),
              ),
              const Icon(Icons.chevron_right, color: T.textFaint),
            ],
          ),
        ),
      );

  Widget _empty() => const Center(
        child: Padding(
          padding: EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              HolocronLogo(size: 128),
              SizedBox(height: 26),
              Text('cost-aware coding, in your pocket',
                  style: T.meta, textAlign: TextAlign.center),
              SizedBox(height: 6),
              Text('Tap New chat to talk to your model directly — no server needed.',
                  style: T.metaFaint, textAlign: TextAlign.center),
            ],
          ),
        ),
      );

  void _openSettings(BuildContext context) => Navigator.push(
      context, MaterialPageRoute(builder: (_) => const SettingsScreen()));

  Future<void> _confirmDelete(BuildContext context, DirectChat c) async {
    final ok = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: T.surface,
      builder: (_) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(
            leading: const Icon(Icons.delete_outline, color: T.danger),
            title: const Text('Delete chat', style: TextStyle(color: T.text)),
            onTap: () => Navigator.pop(context, true),
          ),
        ]),
      ),
    );
    if (ok == true && context.mounted) {
      await context.read<AppState>().deleteChat(c);
    }
  }
}

/// Longer relative time for list rows ("3 days ago").
String relTimeLong(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inMinutes < 1) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes} min ago';
  if (d.inHours < 24) return '${d.inHours}h ago';
  if (d.inDays == 1) return 'yesterday';
  if (d.inDays < 7) return '${d.inDays} days ago';
  if (d.inDays < 30) return '${(d.inDays / 7).floor()} weeks ago';
  return '${(d.inDays / 30).floor()} months ago';
}
