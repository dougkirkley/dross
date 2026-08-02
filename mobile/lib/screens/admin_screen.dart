import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Accounts: who is waiting to be let in, and everyone who already was.
///
/// The server decides who may open this — the app asks `whoami` and hides the
/// entry, but the API refuses a non-admin whatever the client believes. Ordering
/// comes from the server too, pending first, so the thing an admin came for is
/// at the top without the app inventing its own priority.
class AdminScreen extends StatefulWidget {
  const AdminScreen({super.key});
  @override
  State<AdminScreen> createState() => _AdminScreenState();
}

class _AdminScreenState extends State<AdminScreen> {
  late Future<List<Account>> _accounts;
  final Set<String> _deciding = {};

  @override
  void initState() {
    super.initState();
    _accounts = context.read<AppState>().accounts();
  }

  void _reload() =>
      setState(() => _accounts = context.read<AppState>().accounts());

  Future<void> _act(Account a, String what, Future<String?> Function() run) async {
    setState(() => _deciding.add(a.email));
    String? err;
    try {
      err = await run();
    } finally {
      if (mounted) setState(() => _deciding.remove(a.email));
    }
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(err ?? '${a.email} $what'),
      backgroundColor: T.surface,
    ));
    _reload();
  }

  Future<void> _confirmRemove(Account a) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: T.surface,
        title: const Text('Remove account?', style: T.title),
        content: Text(
          '${a.email} will be deleted. Signing in again would make them a new '
          'account waiting for approval - their sessions and workspaces are not '
          'removed with them.',
          style: T.metaFaint,
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel', style: TextStyle(color: T.textDim))),
          TextButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('Remove', style: TextStyle(color: T.danger))),
        ],
      ),
    );
    if (ok != true) return;
    await _act(a, 'removed', () => context.read<AppState>().removeAccount(a.email));
  }

  Future<void> _decide(Account a, String action) async {
    setState(() => _deciding.add(a.email));
    String? note;
    try {
      final mailError = await context.read<AppState>().decideAccount(a.email, action);
      note = mailError == null
          ? '${a.email} is ${action == 'approve' ? 'approved' : 'denied'}'
          // The decision landed; only the telling failed. Say which half, or
          // the natural move is to press the button again.
          : 'Approved, but the welcome mail failed: $mailError';
    } catch (e) {
      note = 'Could not $action ${a.email}: $e';
    } finally {
      if (mounted) setState(() => _deciding.remove(a.email));
    }
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(note), backgroundColor: T.surface));
    _reload();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: T.bg,
      appBar: AppBar(
        backgroundColor: T.bg,
        surfaceTintColor: Colors.transparent,
        title: const Text('Admin', style: T.title),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, color: T.textDim),
            onPressed: _reload,
            tooltip: 'Refresh',
          ),
        ],
      ),
      body: FutureBuilder<List<Account>>(
        future: _accounts,
        builder: (context, snap) {
          if (snap.connectionState != ConnectionState.done) {
            return const Center(
                child: SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(strokeWidth: 2, color: T.accent)));
          }
          if (snap.hasError) {
            return _message('Could not load accounts.\n${snap.error}');
          }
          final all = snap.data ?? const <Account>[];
          if (all.isEmpty) return _message('No accounts yet.');

          final pending = all.where((a) => a.pending).toList();
          final decided = all.where((a) => !a.pending).toList();
          return ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              _heading('Waiting'),
              if (pending.isEmpty)
                _message('Nobody is waiting.', pad: true)
              else
                ...pending.map(_row),
              const SizedBox(height: 12),
              _heading('Everyone else'),
              ...decided.map(_row),
              const SizedBox(height: 24),
            ],
          );
        },
      ),
    );
  }

  Widget _heading(String s) => Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
        child: Text(s.toUpperCase(),
            style: T.metaFaint.copyWith(letterSpacing: 1.1)),
      );

  Widget _message(String s, {bool pad = false}) => Padding(
        padding: EdgeInsets.symmetric(horizontal: 20, vertical: pad ? 8 : 40),
        child: Text(s, style: T.metaFaint, textAlign: pad ? TextAlign.start : TextAlign.center),
      );

  Widget _row(Account a) {
    final busy = _deciding.contains(a.email);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Container(
        decoration: BoxDecoration(
          color: T.surface,
          border: Border.all(color: T.border),
          borderRadius: BorderRadius.circular(T.rCard),
        ),
        padding: const EdgeInsets.fromLTRB(16, 14, 12, 14),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(children: [
                    Flexible(
                      child: Text(a.email,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: T.body.copyWith(fontWeight: FontWeight.w600)),
                    ),
                    if (a.isAdmin) ...[
                      const SizedBox(width: 8),
                      _tag('admin', T.accent),
                    ],
                  ]),
                  const SizedBox(height: 3),
                  Text(_subtitle(a), style: T.metaFaint, maxLines: 1, overflow: TextOverflow.ellipsis),
                ],
              ),
            ),
            const SizedBox(width: 4),
            PopupMenuButton<String>(
              icon: const Icon(Icons.more_vert, size: 18, color: T.textDim),
              color: T.surface,
              onSelected: (v) {
                switch (v) {
                  case 'admin':
                    _act(a, 'is an admin',
                        () => context.read<AppState>().setRole(a.email, 'admin'));
                  case 'user':
                    _act(a, 'is no longer an admin',
                        () => context.read<AppState>().setRole(a.email, 'user'));
                  case 'remove':
                    _confirmRemove(a);
                }
              },
              itemBuilder: (_) => [
                if (!a.isAdmin)
                  const PopupMenuItem(
                      value: 'admin',
                      child: Text('Make admin', style: T.body)),
                if (a.isAdmin)
                  const PopupMenuItem(
                      value: 'user',
                      child: Text('Remove admin', style: T.body)),
                const PopupMenuItem(
                    value: 'remove',
                    child: Text('Remove account',
                        style: TextStyle(color: T.danger, fontSize: 15))),
              ],
            ),
            const SizedBox(width: 4),
            if (busy)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 12),
                child: SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2, color: T.accent)),
              )
            else if (a.pending) ...[
              _button('Approve', primary: true, onTap: () => _decide(a, 'approve')),
              const SizedBox(width: 6),
              _button('Deny', onTap: () => _decide(a, 'deny')),
            ] else if (a.approved)
              // Revoking is the same call as denying. Named for what it does to
              // someone who already had access.
              _button('Revoke', onTap: () => _decide(a, 'deny'))
            else
              _button('Approve', primary: true, onTap: () => _decide(a, 'approve')),
          ],
        ),
      ),
    );
  }

  String _subtitle(Account a) {
    final bits = <String>[];
    if (a.name.isNotEmpty) bits.add(a.name);
    if (a.pending) {
      bits.add('asked ${_ago(a.firstSeen)}');
      bits.add(a.logins == 1 ? '1 sign-in' : '${a.logins} sign-ins');
    } else {
      bits.add('${a.status} · last seen ${_ago(a.lastSeen)}');
      if (a.decidedBy.isNotEmpty) bits.add('by ${a.decidedBy}');
    }
    return bits.join(' · ');
  }

  static String _ago(DateTime? t) {
    if (t == null) return 'never';
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 1) return 'just now';
    if (d.inHours < 1) return '${d.inMinutes}m ago';
    if (d.inDays < 1) return '${d.inHours}h ago';
    return '${d.inDays}d ago';
  }

  Widget _tag(String s, Color c) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
        decoration: BoxDecoration(
          border: Border.all(color: c.withValues(alpha: 0.5)),
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(s, style: T.metaFaint.copyWith(color: c, fontSize: 11)),
      );

  Widget _button(String label, {bool primary = false, required VoidCallback onTap}) =>
      Material(
        color: primary ? T.pill : Colors.transparent,
        borderRadius: BorderRadius.circular(T.rPill),
        child: InkWell(
          borderRadius: BorderRadius.circular(T.rPill),
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
            decoration: primary
                ? null
                : BoxDecoration(
                    border: Border.all(color: T.border),
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
            child: Text(label,
                style: TextStyle(
                  color: primary ? T.pillText : T.textDim,
                  fontSize: 13.5,
                  fontWeight: FontWeight.w600,
                )),
          ),
        ),
      );
}
