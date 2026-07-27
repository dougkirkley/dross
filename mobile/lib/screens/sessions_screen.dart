import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/app_drawer.dart';
import '../widgets/logo.dart';
import 'session_screen.dart';
import 'settings_screen.dart';

/// Remote-mode session list (attaches to `holocron serve`). Mirrors the Claude
/// app's Code page: a Devices card and session cards with status + last message,
/// a near-white "New session" pill.
class SessionsScreen extends StatelessWidget {
  const SessionsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    return Scaffold(
      drawer: const AppDrawer(),
      body: SafeArea(
        child: Column(
          children: [
            FlatHeader(
              title: 'Code',
              leading: Builder(
                builder: (ctx) => CircleIconButton(
                    icon: Icons.menu,
                    onTap: () => Scaffold.of(ctx).openDrawer()),
              ),
              trailing: CircleIconButton(
                  icon: Icons.tune, onTap: () => _settings(context)),
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

  Widget _body(BuildContext context, AppState s) => ListView(
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
            if (s.loadingSessions) const HolocronSpinner(size: 18),
          ]),
          const SizedBox(height: 10),
          if (s.error != null && s.sessions.isEmpty)
            _card(
                child: Padding(
              padding: const EdgeInsets.all(16),
              child: Text('Error: ${s.error}',
                  style: const TextStyle(color: T.danger)),
            )),
          if (s.sessions.isEmpty && s.error == null)
            _card(
                child: const Padding(
              padding: EdgeInsets.all(20),
              child: Text('No sessions yet. Tap New session.',
                  style: TextStyle(color: T.textDim)),
            )),
          for (final sess in s.sessions) ...[
            _sessionCard(context, sess),
            const SizedBox(height: 14),
          ],
        ],
      );

  Widget _sessionCard(BuildContext context, Session sess) => GestureDetector(
        onTap: () async {
          await context.read<AppState>().attach(sess);
          if (context.mounted) {
            Navigator.push(context,
                MaterialPageRoute(builder: (_) => const SessionScreen()));
          }
        },
        child: _card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(children: [
                  _statusIcon(sess.status),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(sess.model,
                        style: T.rowTitle,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis),
                  ),
                  Text('\$${sess.cost.toStringAsFixed(4)}',
                      style: const TextStyle(color: T.accentHi)),
                ]),
                const SizedBox(height: 6),
                Text('${sess.provider} · ${sess.events} events',
                    style: T.metaFaint),
              ],
            ),
          ),
        ),
      );

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
            const Text('Connect to holocron serve', style: T.title),
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

  Future<void> _create(BuildContext context) async {
    final state = context.read<AppState>();
    final Session? s = await state.createSession();
    if (s != null && context.mounted) {
      await state.attach(s);
      if (context.mounted) {
        Navigator.push(context,
            MaterialPageRoute(builder: (_) => const SessionScreen()));
      }
    }
  }
}
