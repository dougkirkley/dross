import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/app_drawer.dart';
import '../widgets/logo.dart';
import 'chat_screen.dart';

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

  Widget _row(BuildContext context, DirectChat c) => Dismissible(
        key: ValueKey(c.id),
        direction: DismissDirection.endToStart,
        background: _swipeBg(),
        confirmDismiss: (_) => _confirmDelete(context, c),
        onDismissed: (_) => context.read<AppState>().deleteChat(c),
        child: InkWell(
          onTap: () {
            context.read<AppState>().openChat(c);
            Navigator.push(context,
                MaterialPageRoute(builder: (_) => const ChatScreen()));
          },
          onLongPress: () async {
            if (await _confirmDelete(context, c) && context.mounted) {
              await context.read<AppState>().deleteChat(c);
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
        ),
      );

  Widget _empty() => Center(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(36, 0, 36, 80),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const HolocronLogo(size: 132),
              const SizedBox(height: 28),
              // Serif tagline (Source Serif) for a calmer, editorial feel.
              Text('Cost-aware coding,\nin your pocket.',
                  style: T.prose.copyWith(
                      fontSize: 22, height: 1.35, color: T.textDim),
                  textAlign: TextAlign.center),
              const SizedBox(height: 14),
              const Text('Talk to your model directly — no server needed.',
                  style: T.metaFaint, textAlign: TextAlign.center),
              const SizedBox(height: 24),
              // Subtle nudge toward the New chat pill.
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

  // Confirms deletion via a bottom sheet; returns true if the user confirmed.
  // The caller performs the delete (swipe dismiss or long-press).
  Future<bool> _confirmDelete(BuildContext context, DirectChat c) async {
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
    return ok == true;
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
