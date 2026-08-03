import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import '../widgets/readable.dart';
import '../util/nav.dart';

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
      // The same header as every other screen, with the same way back. The
      // stock AppBar looked like a different app, and on web its implied back
      // button did not reliably appear — leaving Admin a page with no exit.
      body: SafeArea(
        child: Column(children: [
          FlatHeader(
            title: 'Admin',
            leading: CircleIconButton(
                icon: Icons.chevron_left, onTap: () => backOrHome(context)),
            trailing: CircleIconButton(icon: Icons.refresh, onTap: _reload),
          ),
          Expanded(child: _body()),
        ]),
      ),
    );
  }

  Widget _body() {
    return FutureBuilder<List<Account>>(
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
          // Readable, like the conversations: a card as wide as a desktop
          // browser is mostly empty space with an email lost at one end of it.
          return Readable(
            child: ListView(
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
            ),
          );
        });
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
      child: Material(
        color: T.surface,
        borderRadius: BorderRadius.circular(T.rCard),
        child: InkWell(
          borderRadius: BorderRadius.circular(T.rCard),
          // A card is as wide as a phone, and an address plus a status does not
          // fit on one - so the card shows what identifies somebody and a tap
          // shows the rest, rather than the row eliding the half that matters.
          onTap: () => _showAccount(a),
          child: Container(
            decoration: BoxDecoration(
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
                              style:
                                  T.body.copyWith(fontWeight: FontWeight.w600)),
                        ),
                        if (a.isAdmin) ...[
                          const SizedBox(width: 8),
                          _tag('admin', T.accent),
                        ],
                      ]),
                      const SizedBox(height: 3),
                      Text(_subtitle(a),
                          style: T.metaFaint,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis),
                    ],
                  ),
                ),
                const SizedBox(width: 8),
                // One action on the card — the one an admin came here to press.
                // Roles and removal live on the sheet the card opens, which is
                // where there is room to say what they do. The overflow menu
                // that used to sit here cost the address the width it needed.
                if (busy)
                  const Padding(
                    padding: EdgeInsets.symmetric(horizontal: 12),
                    child: SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: T.accent)),
                  )
                else if (a.pending) ...[
                  _button('Approve',
                      primary: true, onTap: () => _decide(a, 'approve')),
                  const SizedBox(width: 6),
                  _button('Deny', onTap: () => _decide(a, 'deny')),
                ] else if (a.approved)
                  // Revoking is the same call as denying. Named for what it does
                  // to someone who already had access.
                  _button('Revoke', onTap: () => _decide(a, 'deny'))
                else
                  _button('Approve',
                      primary: true, onTap: () => _decide(a, 'approve')),
              ],
            ),
          ),
        ),
      ),
    );
  }

  /// Everything known about one account, in full — the address that the card
  /// had to cut short, and the dates behind "last seen 3d ago".
  ///
  /// The actions come with it. Opening a card to read who somebody is and then
  /// having to close it to act on them is a step that exists for no reason.
  void _showAccount(Account a) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: T.surface,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(T.rCard)),
      ),
      builder: (sheet) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 10, 20, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Center(
                child: Container(
                  width: 36,
                  height: 4,
                  margin: const EdgeInsets.only(bottom: 16),
                  decoration: BoxDecoration(
                    color: T.border,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              // Wrapped, not elided: the address is the one thing on this sheet
              // somebody might need to read character by character.
              SelectionArea(
                child: Text(a.email,
                    style: T.body.copyWith(
                        fontWeight: FontWeight.w600, fontSize: 17)),
              ),
              const SizedBox(height: 10),
              Wrap(spacing: 8, runSpacing: 8, children: [
                _tag(a.status, a.pending
                    ? T.accentHi
                    : a.approved
                        ? T.accent
                        : T.danger),
                if (a.isAdmin) _tag('admin', T.accent),
              ]),
              const SizedBox(height: 16),
              if (a.name.isNotEmpty) _detail('Name', a.name),
              _detail('First seen', _when(a.firstSeen)),
              _detail('Last seen', _when(a.lastSeen)),
              _detail('Sign-ins', '${a.logins}'),
              if (a.decidedBy.isNotEmpty) _detail('Decided by', a.decidedBy),
              const SizedBox(height: 18),
              Wrap(spacing: 8, runSpacing: 8, children: [
                if (a.pending) ...[
                  _button('Approve',
                      primary: true,
                      onTap: () {
                        Navigator.pop(sheet);
                        _decide(a, 'approve');
                      }),
                  _button('Deny', onTap: () {
                    Navigator.pop(sheet);
                    _decide(a, 'deny');
                  }),
                ] else if (a.approved)
                  _button('Revoke', onTap: () {
                    Navigator.pop(sheet);
                    _decide(a, 'deny');
                  })
                else
                  _button('Approve',
                      primary: true,
                      onTap: () {
                        Navigator.pop(sheet);
                        _decide(a, 'approve');
                      }),
                _button(a.isAdmin ? 'Remove admin' : 'Make admin', onTap: () {
                  Navigator.pop(sheet);
                  a.isAdmin
                      ? _act(a, 'is no longer an admin',
                          () => context.read<AppState>().setRole(a.email, 'user'))
                      : _act(a, 'is an admin',
                          () => context.read<AppState>().setRole(a.email, 'admin'));
                }),
                _button('Remove account', danger: true, onTap: () {
                  Navigator.pop(sheet);
                  _confirmRemove(a);
                }),
              ]),
            ],
          ),
        ),
      ),
    );
  }

  Widget _detail(String label, String value) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 5),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
                width: 108,
                child: Text(label, style: T.metaFaint)),
            Expanded(child: Text(value, style: T.body.copyWith(fontSize: 14.5))),
          ],
        ),
      );

  /// A date somebody can act on: when it was, and how long ago that was.
  static String _when(DateTime? t) {
    if (t == null) return 'never';
    String two(int n) => n.toString().padLeft(2, '0');
    return '${t.year}-${two(t.month)}-${two(t.day)} ${two(t.hour)}:${two(t.minute)}'
        ' · ${_ago(t)}';
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

  Widget _button(String label,
          {bool primary = false,
          bool danger = false,
          required VoidCallback onTap}) =>
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
                    border: Border.all(
                        color: danger ? T.danger.withValues(alpha: 0.5) : T.border),
                    borderRadius: BorderRadius.circular(T.rPill),
                  ),
            child: Text(label,
                style: TextStyle(
                  color: primary
                      ? T.pillText
                      : danger
                          ? T.danger
                          : T.textDim,
                  fontSize: 13.5,
                  fontWeight: FontWeight.w600,
                )),
          ),
        ),
      );
}
