import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import 'session_screen.dart';
import 'settings_screen.dart';

class SessionsScreen extends StatelessWidget {
  const SessionsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    return Scaffold(
      appBar: AppBar(
        title: const Text('◆ holocron'),
        actions: [
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => const SettingsScreen()),
            ),
          ),
        ],
      ),
      body: !s.configured
          ? _needsConfig(context)
          : RefreshIndicator(
              onRefresh: s.refreshSessions,
              child: _list(context, s),
            ),
      floatingActionButton: s.configured
          ? FloatingActionButton.extended(
              onPressed: () => _create(context),
              icon: const Icon(Icons.add),
              label: const Text('New'),
            )
          : null,
    );
  }

  Widget _needsConfig(BuildContext context) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('Set your server URL to begin.'),
            const SizedBox(height: 12),
            FilledButton(
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(builder: (_) => const SettingsScreen()),
              ),
              child: const Text('Open Settings'),
            ),
          ],
        ),
      );

  Widget _list(BuildContext context, AppState s) {
    if (s.error != null && s.sessions.isEmpty) {
      return ListView(children: [
        Padding(padding: const EdgeInsets.all(24), child: Text('Error: ${s.error}')),
      ]);
    }
    if (s.sessions.isEmpty) {
      return ListView(children: const [
        Padding(padding: EdgeInsets.all(24), child: Text('No sessions yet. Tap New.')),
      ]);
    }
    return ListView.separated(
      itemCount: s.sessions.length,
      separatorBuilder: (_, __) => const Divider(height: 1),
      itemBuilder: (_, i) {
        final sess = s.sessions[i];
        return ListTile(
          leading: _statusDot(sess.status),
          title: Text(sess.model, maxLines: 1, overflow: TextOverflow.ellipsis),
          subtitle: Text('${sess.provider} · ${sess.events} events'),
          trailing: Text('\$${sess.cost.toStringAsFixed(4)}'),
          onTap: () async {
            await context.read<AppState>().attach(sess);
            if (context.mounted) {
              Navigator.push(context, MaterialPageRoute(builder: (_) => const SessionScreen()));
            }
          },
        );
      },
    );
  }

  Widget _statusDot(String status) {
    final color = switch (status) {
      'running' => const Color(0xFF8FC7E8),
      'error' => const Color(0xFFE06C75),
      _ => const Color(0xFF4B5158),
    };
    return Icon(Icons.circle, size: 12, color: color);
  }

  Future<void> _create(BuildContext context) async {
    final state = context.read<AppState>();
    final Session? s = await state.createSession();
    if (s != null && context.mounted) {
      await state.attach(s);
      if (context.mounted) {
        Navigator.push(context, MaterialPageRoute(builder: (_) => const SessionScreen()));
      }
    }
  }
}
