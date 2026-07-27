// Wire models — match the holocron serve API (internal/server). JSON is
// snake_case; mapping is explicit in fromJson.

class Session {
  final String id;
  final String model;
  final String provider;
  final String? workerModel;
  final String? style;
  final String status; // idle | running | error
  final double cost;
  final int events;

  Session({
    required this.id,
    required this.model,
    required this.provider,
    this.workerModel,
    this.style,
    required this.status,
    required this.cost,
    required this.events,
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
      );

  bool get isRunning => status == 'running';
}

class Event {
  final int seq;
  final String kind; // prompt | text | tool | tool-result | worker:* | done | error | checkpoint
  final String data;

  Event({required this.seq, required this.kind, required this.data});

  factory Event.fromJson(Map<String, dynamic> j) => Event(
        seq: j['seq'] as int,
        kind: j['kind'] as String? ?? '',
        data: j['data'] as String? ?? '',
      );

  bool get isWorker => kind.startsWith('worker:');
  String get baseKind => isWorker ? kind.substring('worker:'.length) : kind;
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
