// Wire + client models. Two worlds:
//   - Direct mode: the phone runs a client-side chat loop straight against a
//     provider (OpenAI-compatible or Anthropic). Messages are ChatMessage.
//   - Remote mode: the phone attaches to `holocron serve`; sessions are
//     server-side and stream Events. Session/Event/PublishResult mirror the
//     serve API (internal/server); JSON is snake_case.

/// How the app reaches a model. Direct is the default (no serve needed).
enum ConnMode { direct, remote }

/// Direct-mode backends.
enum ProviderKind { openai, anthropic }

/// Agent permission mode, mirroring Claude's: how much the agent may do without
/// asking. Meaningful in remote mode (the serve agent has tools); in direct mode
/// there are no tools, so it is informational.
enum PermissionMode { auto, manual, acceptEdits, plan }

extension PermissionModeLabel on PermissionMode {
  String get label => switch (this) {
        PermissionMode.auto => 'Auto',
        PermissionMode.manual => 'Manual',
        PermissionMode.acceptEdits => 'Accept edits',
        PermissionMode.plan => 'Plan',
      };
  String get wire => switch (this) {
        PermissionMode.auto => 'auto',
        PermissionMode.manual => 'manual',
        PermissionMode.acceptEdits => 'accept-edits',
        PermissionMode.plan => 'plan',
      };
  PermissionMode get next =>
      PermissionMode.values[(index + 1) % PermissionMode.values.length];
}

/// A file the user attached to a message. [bytes] is the raw content; [text] is
/// set when the file is small and text-like, so direct mode can inline it.
class Attachment {
  Attachment({required this.name, required this.bytes, this.mime = '', this.text});
  final String name;
  final List<int> bytes;
  final String mime;
  final String? text;

  bool get isImage => mime.startsWith('image/');
}

/// A single turn in a direct-mode conversation.
class ChatMessage {
  ChatMessage({required this.role, this.text = '', this.streaming = false});
  final String role; // 'user' | 'assistant'
  String text;
  bool streaming;

  bool get isUser => role == 'user';

  Map<String, dynamic> toJson() => {'role': role, 'text': text};
  factory ChatMessage.fromJson(Map<String, dynamic> j) =>
      ChatMessage(role: j['role'] as String, text: j['text'] as String? ?? '');
}

/// A persisted direct-mode conversation (the "Chats" list mirrors these).
class DirectChat {
  DirectChat({
    required this.id,
    required this.title,
    required this.messages,
    required this.updated,
  });

  final String id;
  String title;
  final List<ChatMessage> messages;
  DateTime updated;

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'updated': updated.toIso8601String(),
        'messages': messages.map((m) => m.toJson()).toList(),
      };

  factory DirectChat.fromJson(Map<String, dynamic> j) => DirectChat(
        id: j['id'] as String,
        title: j['title'] as String? ?? 'Untitled',
        updated: DateTime.tryParse(j['updated'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
        messages: ((j['messages'] as List?) ?? [])
            .map((e) => ChatMessage.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

// --- remote (serve) models ------------------------------------------------

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

/// Parsed payload of an "edit" event: a file write/edit with line counts and a
/// unified diff (each line prefixed "+ ", "- ", or "  ").
class EditInfo {
  EditInfo({
    required this.file,
    required this.added,
    required this.removed,
    required this.diff,
  });
  final String file;
  final int added;
  final int removed;
  final String diff;

  factory EditInfo.fromJson(Map<String, dynamic> j) => EditInfo(
        file: j['file'] as String? ?? '',
        added: (j['added'] as num?)?.toInt() ?? 0,
        removed: (j['removed'] as num?)?.toInt() ?? 0,
        diff: j['diff'] as String? ?? '',
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
