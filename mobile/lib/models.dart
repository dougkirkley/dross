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
}

class Event {
  final int seq;
  final String kind; // prompt | text | tool | tool-result | worker:* | shell:* | done | error | checkpoint
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
  PublishResult({required this.url, required this.mime, required this.filename});
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

  static DateTime? _time(dynamic v) =>
      v == null ? null : DateTime.tryParse(v as String)?.toLocal();

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
