import 'dart:convert';

// Client models for `dross serve`: the app attaches to server-side sessions and
// streams Events. Session/Event/PublishResult mirror the serve API
// (internal/server); JSON is snake_case.

class Session {
  final String id;
  final String model;
  final String provider;
  final String? workerModel;
  final String? style;
  final String status; // idle | running | error
  final double cost;
  final int events;
  final String title;
  final String workspace;
  final String last;
  final DateTime? updated;
  final String device; // owning device ("" = runs in the pod)
  final bool connected; // for device sessions: is that device linked now

  /// Chat session rather than a code one. Servers may answer chat on their own
  /// backend, so this decides which model list the picker asks for.
  final bool chat;

  Session({
    required this.id,
    required this.model,
    required this.provider,
    this.workerModel,
    this.style,
    required this.status,
    required this.cost,
    required this.events,
    this.title = '',
    this.workspace = '',
    this.last = '',
    this.updated,
    this.device = '',
    this.connected = false,
    this.chat = false,
  });

  factory Session.fromJson(Map<String, dynamic> j) => Session(
        id: j['id'] as String,
        model: j['model'] as String? ?? '',
        provider: j['provider'] as String? ?? '',
        workerModel: j['worker_model'] as String?,
        style: j['style'] as String?,
        status: j['status'] as String? ?? 'idle',
        cost: (j['cost'] as num?)?.toDouble() ?? 0,
        events: j['events'] as int? ?? 0,
        title: j['title'] as String? ?? '',
        workspace: j['workspace'] as String? ?? '',
        last: j['last'] as String? ?? '',
        updated: DateTime.tryParse(j['updated'] as String? ?? ''),
        device: j['device'] as String? ?? '',
        connected: j['connected'] as bool? ?? false,
        chat: j['chat'] as bool? ?? false,
      );

  /// The display name for the session card: the title, or the model as a
  /// fallback for sessions that have not run a prompt yet.
  /// The session's name for display.
  ///
  /// Falls back to a placeholder rather than the model: every session on the
  /// server runs the same model, so naming them all after it told you nothing
  /// and made a list of sessions look like a list of one thing repeated. A
  /// session takes its name from its first prompt, or from a rename.
  String get displayTitle => title.isNotEmpty ? title : 'New session';

  bool get isRunning => status == 'running';

  /// Runs on a machine somebody linked with `dross rc`, rather than in a pod
  /// the server started.
  ///
  /// Both are "device sessions" to the server — the pod attaches over the same
  /// relay — so `device` alone does not separate them. A pod is named after the
  /// session it serves, which is what makes the two tellable apart: anything
  /// else is a laptop.
  ///
  /// They are not the same thing to look at. A pod belongs to the server and
  /// comes back by itself; a laptop is somebody's machine, and when it is gone
  /// the session waits for that person rather than for Kubernetes.
  bool get onLinkedMachine => device.isNotEmpty && device != id;

  /// Runs in a session pod the server provisioned.
  bool get inPod => device.isNotEmpty && device == id;
}

class Event {
  final int seq;
  final String
      kind; // prompt | text | tool | tool-result | worker:* | shell:* | done | error | checkpoint
  final String data;
  final DateTime? time;

  Event({required this.seq, required this.kind, required this.data, this.time});

  factory Event.fromJson(Map<String, dynamic> j) => Event(
        seq: j['seq'] as int,
        kind: j['kind'] as String? ?? '',
        data: j['data'] as String? ?? '',
        time: DateTime.tryParse(j['time'] as String? ?? ''),
      );

  bool get isWorker => kind.startsWith('worker:');
  String get baseKind => isWorker ? kind.substring('worker:'.length) : kind;
  bool get isShell => kind.startsWith('shell:');
}

/// A background shell reconstructed from shell:start/output/exit events.
class ShellTask {
  ShellTask({required this.id, required this.command, this.startedAt});
  final String id;
  final String command;
  final DateTime? startedAt;
  final StringBuffer _output = StringBuffer();
  bool running = true;
  String exit = '';
  DateTime? endedAt;

  String get output => _output.toString();
  void append(String s) => _output.write(s);

  Duration get runtime {
    final start = startedAt;
    if (start == null) return Duration.zero;
    return (endedAt ?? DateTime.now()).difference(start);
  }
}

/// Reconstructs background shells from a session's event list, newest first.
List<ShellTask> buildShells(List<Event> events) {
  final byId = <String, ShellTask>{};
  final order = <String>[];
  ({String id, String rest}) split(String data) {
    final i = data.indexOf('\x1F');
    return i >= 0
        ? (id: data.substring(0, i), rest: data.substring(i + 1))
        : (id: data, rest: '');
  }

  for (final e in events) {
    if (!e.isShell) continue;
    final p = split(e.data);
    switch (e.kind) {
      case 'shell:start':
        if (!byId.containsKey(p.id)) {
          byId[p.id] = ShellTask(id: p.id, command: p.rest, startedAt: e.time);
          order.add(p.id);
        }
      case 'shell:output':
        byId[p.id]?.append(p.rest);
      case 'shell:exit':
        final s = byId[p.id];
        if (s != null) {
          s.running = false;
          s.exit = p.rest;
          s.endedAt = e.time;
        }
    }
  }
  return order.reversed.map((id) => byId[id]!).toList();
}

/// Parsed payload of an "edit" event: a file write/edit with line counts and a
/// unified diff (each line prefixed "+ ", "- ", or "  ").
class EditInfo {
  EditInfo({
    required this.file,
    required this.added,
    required this.removed,
    required this.diff,
    this.isNew = false,
    this.content = '',
  });
  final String file;
  final int added;
  final int removed;
  final String diff;

  /// A brand-new file: [content] holds the file, and there's no diff to show.
  final bool isNew;
  final String content;

  factory EditInfo.fromJson(Map<String, dynamic> j) => EditInfo(
        file: j['file'] as String? ?? '',
        added: (j['added'] as num?)?.toInt() ?? 0,
        removed: (j['removed'] as num?)?.toInt() ?? 0,
        diff: j['diff'] as String? ?? '',
        isNew: j['new'] as bool? ?? false,
        content: j['content'] as String? ?? '',
      );
}

class PublishResult {
  final String url;
  final String mime;
  final String filename;
  PublishResult(
      {required this.url, required this.mime, required this.filename});
  factory PublishResult.fromJson(Map<String, dynamic> j) => PublishResult(
        url: j['url'] as String,
        mime: j['mime'] as String? ?? '',
        filename: j['filename'] as String? ?? '',
      );
}

/// Account is a registered user as the admin surface sees it. Mirrors what
/// `GET /api/admin/users` returns; the server decides the order (whoever is
/// waiting first), so the app does not re-sort and invent its own priority.
class Account {
  final String email;
  final String name;
  final String status; // pending | approved | denied
  final String role; // user | admin
  final DateTime? firstSeen;
  final DateTime? lastSeen;
  final int logins;
  final String decidedBy;

  Account({
    required this.email,
    this.name = '',
    required this.status,
    this.role = 'user',
    this.firstSeen,
    this.lastSeen,
    this.logins = 0,
    this.decidedBy = '',
  });

  bool get pending => status == 'pending';
  bool get approved => status == 'approved';
  bool get isAdmin => role == 'admin';

  /// Parses a server timestamp, treating Go's zero time as absent.
  ///
  /// An account that has never signed in carries "0001-01-01T00:00:00Z", which
  /// parses happily and then renders as "last seen 739830d ago" — a number
  /// that reads as a bug about someone rather than a fact about nobody.
  static DateTime? _time(dynamic v) {
    if (v == null) return null;
    final t = DateTime.tryParse(v as String);
    if (t == null || t.year < 2000) return null;
    return t.toLocal();
  }

  factory Account.fromJson(Map<String, dynamic> j) => Account(
        email: j['email'] as String? ?? '',
        name: j['name'] as String? ?? '',
        status: j['status'] as String? ?? 'pending',
        role: j['role'] as String? ?? 'user',
        firstSeen: _time(j['first_seen']),
        lastSeen: _time(j['last_seen']),
        logins: (j['logins'] as num?)?.toInt() ?? 0,
        decidedBy: j['decided_by'] as String? ?? '',
      );
}

/// One thing a turn actually consulted.
///
/// Built server-side from the tools that fetched it, never from the model's
/// prose — a local model asked to list its sources invents plausible URLs, and
/// a fabricated citation is worse than none because it looks checkable.
class Source {
  final String title;
  final String url;
  final String snippet;
  final String site;

  Source(
      {required this.url, this.title = '', this.snippet = '', this.site = ''});

  /// What to show when a page had no title: the site, then the bare URL.
  String get label => title.isNotEmpty ? title : (site.isNotEmpty ? site : url);

  factory Source.fromJson(Map<String, dynamic> j) => Source(
        title: j['title'] as String? ?? '',
        url: j['url'] as String? ?? '',
        snippet: j['snippet'] as String? ?? '',
        site: j['site'] as String? ?? '',
      );

  /// Parses a `sources` event payload, tolerating anything malformed by
  /// returning nothing: a broken citation list should cost the answer nothing.
  static List<Source> listFrom(String data) {
    try {
      final raw = jsonDecode(data);
      if (raw is! List) return const [];
      return raw
          .whereType<Map<String, dynamic>>()
          .map(Source.fromJson)
          .where((s) => s.url.isNotEmpty)
          .toList();
    } catch (_) {
      return const [];
    }
  }
}

/// What a session's model picker should show: the ids the server offers, and
/// which of them that backend has resident right now.
///
/// Loading a model that is not resident costs a cold load — a minute or more
/// against a local model — and a backend that holds one at a time unloads
/// whatever was there, for everyone using it. Knowing which is warm is the
/// difference between choosing a model and discovering the cost afterwards.
class ModelChoices {
  const ModelChoices({
    this.models = const [],
    this.current = '',
    this.loaded = const [],
  });

  final List<String> models;
  final String current;

  /// Resident on the backend now. Empty from a server that could not be asked
  /// as well as from one with nothing loaded — so it is a reason to say more,
  /// never a reason to claim a model is cold.
  final List<String> loaded;

  /// ollama reports "devstral:latest" where a config says "devstral"; they are
  /// one model spelled two ways, and comparing them literally showed the loaded
  /// model as cold.
  static String _same(String id) {
    final s = id.trim();
    return s.endsWith(':latest') ? s.substring(0, s.length - 7) : s;
  }

  bool isLoaded(String model) => loaded.any((m) => _same(m) == _same(model));

  /// Whether choosing [model] would unload something somebody else may be
  /// using. True only when the backend has said what it holds, that model is
  /// not among it, and something else is.
  bool evicts(String model) => loaded.isNotEmpty && !isLoaded(model);

  static List<String> _strings(dynamic v) =>
      (v as List?)?.map((e) => e.toString()).toList() ?? const [];

  factory ModelChoices.fromJson(Map<String, dynamic> j) => ModelChoices(
        models: _strings(j['models']),
        current: j['current'] as String? ?? '',
        loaded: _strings(j['loaded']),
      );
}

/// A git host the server can start sessions from, and whether this account has
/// connected it.
///
/// The server answers all three fields together because they are one question:
/// a picker that knows a host exists but not whether it is connected has
/// nothing useful to draw.
class ForgeHost {
  ForgeHost({
    required this.name,
    required this.connected,
    this.installUrl = '',
  });

  /// "github" or "gitlab" — the id used in URLs, not a display name.
  final String name;
  final bool connected;

  /// Where to install the app so it can see repositories. GitHub only:
  /// authorizing and installing are two steps there, and doing only the first
  /// leaves an account that is connected and can see nothing.
  final String installUrl;

  String get label => name.isEmpty
      ? name
      : '${name[0].toUpperCase()}${name.substring(1)}'
          .replaceAll('Github', 'GitHub')
          .replaceAll('Gitlab', 'GitLab');

  factory ForgeHost.fromJson(Map<String, dynamic> j) => ForgeHost(
        name: j['name'] as String? ?? '',
        connected: j['connected'] as bool? ?? false,
        installUrl: j['install_url'] as String? ?? '',
      );
}

/// One repository a session can start on.
class Repo {
  Repo({
    required this.fullName,
    this.branch = '',
    this.private = false,
    this.updated,
  });

  /// "owner/name", which is what a person recognises and what the server takes
  /// back when a session is created. The clone URL is deliberately not here:
  /// the server resolves it from the host, so the app never chooses the address
  /// a credential is sent to.
  final String fullName;
  final String branch;
  final bool private;
  final DateTime? updated;

  String get owner => fullName.contains('/') ? fullName.split('/').first : '';
  String get name =>
      fullName.contains('/') ? fullName.split('/').last : fullName;

  factory Repo.fromJson(Map<String, dynamic> j) => Repo(
        fullName: j['full_name'] as String? ?? '',
        branch: j['branch'] as String? ?? '',
        private: j['private'] as bool? ?? false,
        updated: DateTime.tryParse(j['updated'] as String? ?? ''),
      );
}

/// What one host can offer right now: its repositories, or the reason there are
/// none to show.
class RepoList {
  RepoList({required this.repos, this.installUrl = ''});

  final List<Repo> repos;

  /// Set when the account authorized the app but has not installed it on
  /// anything. An empty list with somewhere to go, rather than an error.
  final String installUrl;

  factory RepoList.fromJson(Map<String, dynamic> j) => RepoList(
        repos: ((j['repos'] as List?) ?? [])
            .map((e) => Repo.fromJson(e as Map<String, dynamic>))
            .toList(),
        installUrl: j['install_url'] as String? ?? '',
      );
}
