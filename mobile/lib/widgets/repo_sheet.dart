import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../api.dart';
import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import 'logo.dart';
import 'modal.dart';

/// What a new session should start on.
class RepoChoice {
  const RepoChoice({this.forge, this.repo});

  /// An empty workspace, which is what every session was before git hosts
  /// existed and is still the right answer for work that is not in a
  /// repository.
  const RepoChoice.empty()
      : forge = null,
        repo = null;

  final String? forge;
  final String? repo;

  bool get isEmpty => repo == null;
}

/// Asks what to start a session on: an empty workspace, or a repository from a
/// connected git host.
///
/// Returns null if the sheet was dismissed, which is a cancelled create rather
/// than a choice of nothing — the two are different and only one of them should
/// make a session.
Future<RepoChoice?> showRepoSheet(BuildContext context) {
  return showResponsiveModal<RepoChoice>(
    context: context,
    builder: (_, dialog) => _RepoSheet(dialog: dialog),
  );
}

class _RepoSheet extends StatefulWidget {
  const _RepoSheet({this.dialog = false});
  final bool dialog;

  @override
  State<_RepoSheet> createState() => _RepoSheetState();
}

class _RepoSheetState extends State<_RepoSheet> {
  List<ForgeHost>? _hosts; // null = still asking the server
  String _host = ''; // the one being shown
  RepoList? _repos; // null = not loaded for _host yet
  String? _error;
  bool _loadingRepos = false;
  String _filter = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final hosts = await context.read<AppState>().forges();
    if (!mounted) return;
    // Open on a connected host where there is one: that is the case where
    // something can be picked without another tap.
    final connected = hosts.where((h) => h.connected).toList();
    setState(() {
      _hosts = hosts;
      _host = connected.isNotEmpty
          ? connected.first.name
          : (hosts.isNotEmpty ? hosts.first.name : '');
    });
    if (connected.isNotEmpty) await _loadRepos();
  }

  Future<void> _loadRepos() async {
    if (_host.isEmpty) return;
    setState(() {
      _loadingRepos = true;
      _error = null;
      _repos = null;
    });
    try {
      final list = await context.read<AppState>().forgeRepos(_host);
      if (!mounted) return;
      setState(() {
        _repos = list;
        _loadingRepos = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingRepos = false;
        // A 403 is the credential being gone or past renewing. To a person
        // those are one thing with one fix, so it reads as the invitation
        // rather than as a status code.
        _error = e is ApiException && e.status == 403
            ? 'That connection has lapsed. Connect again to see repositories.'
            : e.toString();
      });
    }
  }

  ForgeHost? get _current {
    for (final h in _hosts ?? const <ForgeHost>[]) {
      if (h.name == _host) return h;
    }
    return null;
  }

  List<Repo> get _shown {
    final all = _repos?.repos ?? const <Repo>[];
    if (_filter.isEmpty) return all;
    final needle = _filter.toLowerCase();
    return all.where((r) => r.fullName.toLowerCase().contains(needle)).toList();
  }

  @override
  Widget build(BuildContext context) {
    final hosts = _hosts;
    return SafeArea(
      child: Container(
        decoration: modalDecoration(widget.dialog),
        constraints:
            BoxConstraints(maxHeight: MediaQuery.of(context).size.height * 0.8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
              child: Row(children: [
                CircleIconButton(
                    icon: Icons.close, onTap: () => Navigator.pop(context)),
                const Expanded(
                    child: Text('Start on',
                        style: T.title, textAlign: TextAlign.center)),
                const SizedBox(width: 44),
              ]),
            ),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                padding: const EdgeInsets.all(16),
                children: [
                  _emptyOption(),
                  if (hosts == null)
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: 24),
                      child: Center(child: DrossSpinner(size: 24)),
                    )
                  else if (hosts.isEmpty)
                    const Padding(
                      padding: EdgeInsets.only(top: 18),
                      child: Text(
                          'This server has no git hosts configured, so a '
                          'session starts on an empty workspace.',
                          style: T.metaFaint),
                    )
                  else ...[
                    if (hosts.length > 1) ...[
                      const SizedBox(height: 18),
                      Wrap(
                        spacing: 8,
                        children: [
                          for (final h in hosts)
                            OutlineChip(
                              filled: h.name == _host,
                              onTap: () {
                                setState(() => _host = h.name);
                                if (h.connected) _loadRepos();
                              },
                              child: Text(h.label,
                                  style: const TextStyle(
                                      color: T.text, fontSize: 13)),
                            ),
                        ],
                      ),
                    ],
                    const SizedBox(height: 18),
                    ..._hostBody(),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _emptyOption() => _Row(
        icon: Icons.folder_outlined,
        title: 'Empty workspace',
        subtitle: 'No repository. The session starts with nothing in it.',
        onTap: () => Navigator.pop(context, const RepoChoice.empty()),
      );

  List<Widget> _hostBody() {
    final host = _current;
    if (host == null) return const [];
    if (!host.connected) {
      return [
        Text('${host.label} is not connected to this account.',
            style: T.metaFaint),
        const SizedBox(height: 14),
        Align(
          alignment: Alignment.centerLeft,
          child: PillButton(
            label: 'Connect ${host.label}',
            icon: Icons.link,
            onTap: () => context.read<AppState>().connectForge(host.name),
          ),
        ),
        const SizedBox(height: 10),
        const Text(
            'This opens the host in a browser. Come back when it is done and '
            'pull down to refresh.',
            style: T.metaFaint),
      ];
    }
    if (_loadingRepos) {
      return const [
        Padding(
          padding: EdgeInsets.symmetric(vertical: 24),
          child: Center(child: DrossSpinner(size: 24)),
        )
      ];
    }
    if (_error != null) {
      return [
        Text(_error!, style: const TextStyle(color: T.danger, fontSize: 13)),
        const SizedBox(height: 14),
        Align(
          alignment: Alignment.centerLeft,
          child: PillButton(
            label: 'Connect ${host.label}',
            icon: Icons.link,
            onTap: () => context.read<AppState>().connectForge(host.name),
          ),
        ),
      ];
    }
    final repos = _repos;
    if (repos == null) return const [];
    // Authorized but the app was never installed anywhere, which is a GitHub
    // step people miss. An empty list explains nothing; the link fixes it.
    if (repos.repos.isEmpty) {
      final install =
          repos.installUrl.isNotEmpty ? repos.installUrl : host.installUrl;
      return [
        Text('${host.label} is connected but can see no repositories.',
            style: T.metaFaint),
        if (install.isNotEmpty) ...[
          const SizedBox(height: 14),
          Align(
            alignment: Alignment.centerLeft,
            child: PillButton(
              label: 'Choose repositories',
              icon: Icons.open_in_new,
              onTap: () => launchUrl(Uri.parse(install),
                  mode: LaunchMode.externalApplication),
            ),
          ),
          const SizedBox(height: 10),
          const Text(
              'Pick which repositories this app may see, then pull down to '
              'refresh.',
              style: T.metaFaint),
        ],
      ];
    }
    return [
      // A search field once the list is long enough to scroll past what you
      // wanted. Below that it is one more thing to look at.
      if (repos.repos.length > 8) ...[
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
          decoration: BoxDecoration(
            color: T.bg,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: T.border),
          ),
          child: TextField(
            autocorrect: false,
            style: T.body,
            onChanged: (v) => setState(() => _filter = v.trim()),
            decoration: const InputDecoration.collapsed(
                hintText: 'Filter repositories',
                hintStyle: TextStyle(color: T.textFaint)),
          ),
        ),
        const SizedBox(height: 12),
      ],
      for (final r in _shown)
        _Row(
          icon: r.private ? Icons.lock_outline : Icons.folder_copy_outlined,
          title: r.fullName,
          subtitle: r.branch.isEmpty ? '' : r.branch,
          onTap: () => Navigator.pop(
              context, RepoChoice(forge: host.name, repo: r.fullName)),
        ),
      if (_shown.isEmpty)
        const Padding(
          padding: EdgeInsets.only(top: 8),
          child: Text('Nothing matches that.', style: T.metaFaint),
        ),
    ];
  }
}

/// One tappable line: an icon, what it is, and what that means.
class _Row extends StatelessWidget {
  const _Row({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(children: [
          Icon(icon, size: 18, color: T.textDim),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: T.rowTitle,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
                if (subtitle.isNotEmpty)
                  Text(subtitle,
                      style: T.metaFaint,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis),
              ],
            ),
          ),
          const Icon(Icons.chevron_right, size: 18, color: T.textFaint),
        ]),
      ),
    );
  }
}
