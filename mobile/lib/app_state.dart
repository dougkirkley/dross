import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:flutter/foundation.dart';
import 'package:flutter_web_auth_2/flutter_web_auth_2.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';
import 'direct/provider.dart';
import 'models.dart';
import 'util/file_pick.dart';

/// AppState is the single source of truth for both worlds:
///   - Direct mode (default): client-side chat against a provider, with an
///     editable send queue while a reply streams.
///   - Remote mode: attach to `holocron serve` (server-side sessions, SSE).
/// Non-secret config lives in SharedPreferences; API keys and OAuth tokens live
/// in the platform secure store.
class AppState extends ChangeNotifier {
  final _secure = const FlutterSecureStorage();

  // --- config ---------------------------------------------------------
  ConnMode mode = ConnMode.direct;

  // direct: provider selection + per-provider config
  ProviderKind providerKind = ProviderKind.openai;
  String openaiBaseUrl = 'https://ollama.farmerjohnparmesan.com/v1';
  String openaiKey = '';
  String openaiModel = 'qwen3-coder:30b';

  String anthropicBaseUrl = 'https://api.anthropic.com';
  String anthropicKey = '';
  String anthropicModel = 'claude-opus-5';

  /// Allowed models offered in the picker per provider. Empty = fall back to
  /// the built-in presets. Editable under Settings (comma-separated).
  List<String> openaiModels = [];
  List<String> anthropicModels = [];

  String identity = '';

  // Agent permission mode (Claude-style). Enforced by the serve agent in remote
  // mode; informational in direct mode (no tools).
  PermissionMode permissionMode = PermissionMode.auto;

  // remote (serve)
  String baseUrl = '';
  String token = '';

  // --- direct state ---------------------------------------------------
  final List<DirectChat> chats = [];
  DirectChat? currentChat;

  /// Messages typed while a reply is streaming. Editable: an item can be
  /// dropped back out before it fires.
  final List<String> queue = [];
  StreamSubscription<String>? _directSub;

  /// Files attached to the next message (chips shown above the input).
  final List<Attachment> pending = [];

  /// Test seam: when set, [_startTurn] uses this instead of building a real
  /// provider from config, so streaming + queue behaviour can be driven without
  /// a network.
  @visibleForTesting
  Future<DirectProvider> Function()? providerOverride;

  // --- remote state (unchanged behaviour) -----------------------------
  List<Session> sessions = [];
  bool loadingSessions = false;
  Session? current;
  final List<Event> events = [];
  StreamSubscription<Event>? _sub;

  String? error;

  bool get isDirect => mode == ConnMode.direct;
  bool get remoteConfigured => baseUrl.isNotEmpty;
  bool get streaming =>
      currentChat?.messages.isNotEmpty == true &&
      currentChat!.messages.last.streaming;

  String get currentModel => switch (providerKind) {
        ProviderKind.openai => openaiModel,
        ProviderKind.anthropic => anthropicModel,
      };

  /// Allowed models for the active provider (empty = use built-in presets).
  List<String> get allowedModels => switch (providerKind) {
        ProviderKind.openai => openaiModels,
        ProviderKind.anthropic => anthropicModels,
      };

  HolocronClient get _client => HolocronClient(baseUrl: baseUrl, token: token);

  // --- load / save ----------------------------------------------------

  Future<void> load() async {
    final p = await SharedPreferences.getInstance();
    // Always open on Chats; the sidebar selection follows `mode`, so restoring
    // a persisted `remote` here left Code highlighted on the Chats page.
    mode = ConnMode.direct;
    providerKind = ProviderKind.values[p.getInt('providerKind') ?? 0];
    openaiBaseUrl = p.getString('openaiBaseUrl') ?? openaiBaseUrl;
    openaiModel = p.getString('openaiModel') ?? openaiModel;
    anthropicBaseUrl = p.getString('anthropicBaseUrl') ?? anthropicBaseUrl;
    anthropicModel = p.getString('anthropicModel') ?? anthropicModel;
    openaiModels = p.getStringList('openaiModels') ?? [];
    anthropicModels = p.getStringList('anthropicModels') ?? [];
    identity = p.getString('identity') ?? '';
    permissionMode = PermissionMode.values[p.getInt('permissionMode') ?? 0];
    baseUrl = p.getString('baseUrl') ?? '';
    // secrets
    openaiKey = await _secure.read(key: 'openaiKey') ?? '';
    anthropicKey = await _secure.read(key: 'anthropicKey') ?? '';
    token = await _secure.read(key: 'remoteToken') ?? '';
    // chats
    final raw = p.getString('chats');
    if (raw != null) {
      chats
        ..clear()
        ..addAll((jsonDecode(raw) as List)
            .map((e) => DirectChat.fromJson(e as Map<String, dynamic>)));
      chats.sort((a, b) => b.updated.compareTo(a.updated));
    }
    notifyListeners();
    if (!isDirect && remoteConfigured) await refreshSessions();
  }

  Future<void> saveConfig() async {
    final p = await SharedPreferences.getInstance();
    await p.setInt('providerKind', providerKind.index);
    await p.setString('openaiBaseUrl', openaiBaseUrl);
    await p.setString('openaiModel', openaiModel);
    await p.setString('anthropicBaseUrl', anthropicBaseUrl);
    await p.setString('anthropicModel', anthropicModel);
    await p.setStringList('openaiModels', openaiModels);
    await p.setStringList('anthropicModels', anthropicModels);
    await p.setString('identity', identity);
    await p.setInt('permissionMode', permissionMode.index);
    await p.setString('baseUrl', baseUrl);
    await _secure.write(key: 'openaiKey', value: openaiKey);
    await _secure.write(key: 'anthropicKey', value: anthropicKey);
    await _secure.write(key: 'remoteToken', value: token);
    notifyListeners();
    if (!isDirect && remoteConfigured) await refreshSessions();
  }

  Future<void> _persistChats() async {
    final p = await SharedPreferences.getInstance();
    await p.setString(
        'chats', jsonEncode(chats.map((c) => c.toJson()).toList()));
  }

  // --- direct chat ----------------------------------------------------

  Future<DirectProvider> _buildProvider() async {
    switch (providerKind) {
      case ProviderKind.openai:
        return OpenAICompatProvider(
            baseUrl: openaiBaseUrl,
            apiKey: openaiKey,
            model: openaiModel,
            system: identity);
      case ProviderKind.anthropic:
        if (anthropicKey.isEmpty) {
          throw Exception('set an Anthropic API key under Settings');
        }
        return AnthropicProvider(
            model: anthropicModel,
            apiKey: anthropicKey,
            baseUrl: anthropicBaseUrl,
            identity: identity);
    }
  }

  DirectChat newChat() {
    final c = DirectChat(
      id: 'c${DateTime.now().microsecondsSinceEpoch}',
      title: 'New chat',
      messages: [],
      updated: DateTime.now(),
    );
    chats.insert(0, c);
    currentChat = c;
    queue.clear();
    notifyListeners();
    return c;
  }

  void openChat(DirectChat c) {
    _directSub?.cancel();
    currentChat = c;
    queue.clear();
    notifyListeners();
  }

  Future<void> deleteChat(DirectChat c) async {
    chats.remove(c);
    if (currentChat == c) currentChat = null;
    await _persistChats();
    notifyListeners();
  }

  /// Switches between direct (Chats) and remote (Code) views. Not persisted:
  /// the app always opens on Chats.
  void setMode(ConnMode m) {
    if (mode == m) return;
    mode = m;
    notifyListeners();
    if (m == ConnMode.remote && remoteConfigured) refreshSessions();
  }

  /// Sets the model for the active direct provider and persists it.
  void setModel(String m) {
    m = m.trim();
    if (m.isEmpty) return;
    switch (providerKind) {
      case ProviderKind.openai:
        openaiModel = m;
      case ProviderKind.anthropic:
        anthropicModel = m;
    }
    notifyListeners();
    saveConfig();
  }

  /// Selects a provider and model together (from the grouped model picker),
  /// then persists.
  void setProviderModel(ProviderKind kind, String model) {
    model = model.trim();
    if (model.isEmpty) return;
    providerKind = kind;
    switch (kind) {
      case ProviderKind.openai:
        openaiModel = model;
      case ProviderKind.anthropic:
        anthropicModel = model;
    }
    notifyListeners();
    saveConfig();
  }

  /// Allowed models for a specific provider (empty = built-in presets).
  List<String> modelsFor(ProviderKind kind) => switch (kind) {
        ProviderKind.openai => openaiModels,
        ProviderKind.anthropic => anthropicModels,
      };

  /// Queries the OpenAI-compatible endpoint (e.g. ollama) for the models it
  /// actually serves, so the picker never offers unavailable ones. Returns an
  /// empty list on any error (caller falls back to the configured/preset list).
  Future<List<String>> availableOpenAIModels() async {
    if (openaiBaseUrl.isEmpty) return [];
    try {
      final r = await http.get(
        Uri.parse('$openaiBaseUrl/models'),
        headers: {
          if (openaiKey.isNotEmpty) 'Authorization': 'Bearer $openaiKey',
        },
      ).timeout(const Duration(seconds: 6));
      if (r.statusCode != 200) return [];
      final body = jsonDecode(r.body) as Map<String, dynamic>;
      final data = (body['data'] as List?) ?? const [];
      final ids = <String>[];
      for (final m in data) {
        final id = (m as Map<String, dynamic>)['id'] as String?;
        if (id != null && id.isNotEmpty) ids.add(id);
      }
      ids.sort();
      return ids;
    } catch (_) {
      return [];
    }
  }

  void setPermissionMode(PermissionMode m) {
    permissionMode = m;
    notifyListeners();
    SharedPreferences.getInstance()
        .then((p) => p.setInt('permissionMode', m.index));
  }

  void addAttachment(Attachment a) {
    pending.add(a);
    notifyListeners();
  }

  void removeAttachment(int i) {
    if (i >= 0 && i < pending.length) {
      pending.removeAt(i);
      notifyListeners();
    }
  }

  /// Folds attachments into the message text. Text-like files are inlined in a
  /// fenced block; other files get a marker (direct-mode providers here are
  /// text-only). Remote mode uploads files separately.
  String _composeWithAttachments(String text, List<Attachment> atts) {
    if (atts.isEmpty) return text;
    final b = StringBuffer();
    for (final a in atts) {
      if (a.text != null && a.text!.isNotEmpty) {
        b.writeln('Attached file `${a.name}`:\n```\n${a.text}\n```\n');
      } else {
        b.writeln(
            '[attached ${a.name}${a.mime.isEmpty ? '' : ' (${a.mime})'} — not inlined]\n');
      }
    }
    b.write(text);
    return b.toString().trim();
  }

  /// Sends [text] (plus any pending attachments) in the current chat. Direct
  /// chat does not queue: while a reply streams, sending is disabled (use the
  /// stop button), so a send while streaming is a no-op.
  Future<void> sendDirect(String text) async {
    if (streaming) return;
    final atts = List<Attachment>.from(pending);
    final composed = _composeWithAttachments(text.trim(), atts);
    if (composed.isEmpty) return;
    pending.clear();
    currentChat ??= newChat();
    await _startTurn(composed);
  }

  /// Removes queued message [i] and returns its text so the UI can drop it back
  /// into the input for editing ("drop out of the queue").
  String removeQueued(int i) {
    if (i < 0 || i >= queue.length) return '';
    final s = queue.removeAt(i);
    notifyListeners();
    return s;
  }

  /// Promotes queued message [i] to the current turn: interrupts the in-flight
  /// reply and immediately runs the queued message. Mirrors the TUI's Ctrl+C
  /// behaviour (default promotes the head of the queue).
  void promoteQueued([int i = 0]) {
    if (i < 0 || i >= queue.length) return;
    final text = queue.removeAt(i);
    _directSub?.cancel();
    _directSub = null;
    final m = currentChat?.messages;
    if (m != null && m.isNotEmpty && m.last.streaming) {
      m.last.streaming = false;
      if (m.last.text.isEmpty) m.last.text = '(interrupted)';
    }
    _startTurn(text);
  }

  Future<void> _startTurn(String userText) async {
    final chat = currentChat!;
    chat.messages.add(ChatMessage(role: 'user', text: userText));
    final assistant = ChatMessage(role: 'assistant', streaming: true);
    chat.messages.add(assistant);
    if (chat.title == 'New chat' || chat.title.isEmpty) {
      chat.title =
          userText.length > 48 ? '${userText.substring(0, 48)}…' : userText;
    }
    chat.updated = DateTime.now();
    _bumpChat(chat);
    notifyListeners();

    try {
      final prov = await (providerOverride ?? _buildProvider)();
      // History excludes the still-empty streaming assistant turn.
      final history = chat.messages.sublist(0, chat.messages.length - 1);
      _directSub = prov.stream(history).listen(
        (delta) {
          assistant.text += delta;
          notifyListeners();
        },
        onDone: () => _finishTurn(assistant),
        onError: (e) {
          assistant.text += assistant.text.isEmpty ? '' : '\n\n';
          assistant.text += '⚠ $e';
          _finishTurn(assistant);
        },
        cancelOnError: true,
      );
    } catch (e) {
      assistant.text = '⚠ $e';
      _finishTurn(assistant);
    }
  }

  void _finishTurn(ChatMessage assistant) {
    assistant.streaming = false;
    _directSub = null;
    _persistChats();
    notifyListeners();
  }

  void interruptDirect() {
    _directSub?.cancel();
    _directSub = null;
    final m = currentChat?.messages;
    if (m != null && m.isNotEmpty && m.last.streaming) {
      m.last.streaming = false;
      if (m.last.text.isEmpty) m.last.text = '(interrupted)';
    }
    _persistChats();
    notifyListeners();
  }

  void _bumpChat(DirectChat c) {
    chats.remove(c);
    chats.insert(0, c);
  }

  // --- remote (serve) -------------------------------------------------

  /// Files uploaded to the current remote session's workspace, awaiting the next
  /// prompt (which references them so the agent can read them).
  final List<({String name, String path})> remotePending = [];

  /// Uploads [f] into the server workspace and stages it for the next prompt.
  /// Returns an error string, or null on success.
  Future<String?> attachRemote(PickedFile f) async {
    try {
      final path = await _client.upload(f.bytes, f.name);
      remotePending.add((name: f.name, path: path));
      notifyListeners();
      return null;
    } catch (e) {
      error = e.toString();
      notifyListeners();
      return e.toString();
    }
  }

  void removeRemotePending(int i) {
    if (i >= 0 && i < remotePending.length) {
      remotePending.removeAt(i);
      notifyListeners();
    }
  }

  String _withRemoteAttachments(String text) {
    if (remotePending.isEmpty) return text;
    final b = StringBuffer('Files uploaded to the workspace:\n');
    for (final a in remotePending) {
      b.writeln('- ${a.path}');
    }
    b.writeln();
    b.write(text);
    return b.toString();
  }

  /// Runs the server's Google SSO flow and stores the returned session token as
  /// the remote bearer. On native it opens an external browser (custom-scheme
  /// callback); on web the same-origin session cookie already covers API calls,
  /// so it just refreshes. Returns an error string, or null on success.
  Future<String?> signInRemote() async {
    if (baseUrl.isEmpty) return 'Set the server URL first';
    if (kIsWeb) {
      await refreshSessions();
      return null;
    }
    try {
      final result = await FlutterWebAuth2.authenticate(
        url: '$baseUrl/auth/login?mode=app',
        callbackUrlScheme: 'holocron',
      );
      final session = Uri.parse(result).queryParameters['session'];
      if (session == null || session.isEmpty) return 'No session returned';
      token = session;
      await _secure.write(key: 'remoteToken', value: token);
      notifyListeners();
      await refreshSessions();
      return null;
    } catch (e) {
      return e.toString();
    }
  }

  Future<void> refreshSessions() async {
    loadingSessions = true;
    error = null;
    notifyListeners();
    try {
      sessions = await _client.listSessions();
    } catch (e) {
      error = e.toString();
    } finally {
      loadingSessions = false;
      notifyListeners();
    }
  }

  Future<Session?> createSession({String? model, String? workerModel}) async {
    try {
      final s =
          await _client.createSession(model: model, workerModel: workerModel);
      await refreshSessions();
      return s;
    } catch (e) {
      error = e.toString();
      notifyListeners();
      return null;
    }
  }

  Future<void> deleteSession(Session s) async {
    // Remove locally first so the list updates immediately, then tell the server.
    sessions.removeWhere((x) => x.id == s.id);
    if (current?.id == s.id) {
      await _sub?.cancel();
      current = null;
      events.clear();
      queue.clear();
    }
    notifyListeners();
    try {
      await _client.deleteSession(s.id);
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
  }

  /// Models the server offers, for the code-session model picker.
  Future<List<String>> availableServerModels() async {
    try {
      return await _client.serverModels();
    } catch (_) {
      return [];
    }
  }

  /// Changes the current remote session's model (applies to the next prompt).
  Future<void> setSessionModel(String model) async {
    final s = current;
    if (s == null || model.trim().isEmpty || model == s.model) return;
    try {
      final updated = await _client.updateSessionModel(s.id, model.trim());
      current = updated;
      final i = sessions.indexWhere((x) => x.id == updated.id);
      if (i >= 0) sessions[i] = updated;
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
  }

  Future<void> attach(Session s) async {
    // Fire-and-forget cancel: on web the SSE stream (BrowserClient) may never
    // complete, so awaiting cancel() could hang and block re-entry.
    _sub?.cancel();
    _sub = null;
    current = s;
    events.clear();
    queue.clear();
    remotePending.clear();
    notifyListeners();
    try {
      events.addAll(await _client.events(s.id));
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
    _startStream();
  }

  void _startStream() {
    final s = current;
    if (s == null) return;
    final after = events.isEmpty ? 0 : events.last.seq;
    _sub = _client.streamEvents(s.id, after: after).listen(
      (ev) {
        if (events.isEmpty || ev.seq > events.last.seq) {
          events.add(ev);
          if (ev.kind == 'done' || ev.kind == 'error') {
            _refreshCurrent().then((_) => _drainQueue());
          }
          notifyListeners();
        }
      },
      onDone: () {
        if (current?.id == s.id) _startStream();
      },
      onError: (_) {},
      cancelOnError: false,
    );
  }

  Future<void> _refreshCurrent() async {
    final s = current;
    if (s == null) return;
    try {
      current = await _client.getSession(s.id);
      notifyListeners(); // reflect the new status (e.g. running -> idle)
    } catch (_) {}
  }

  /// Sends [text] to the current remote session. If the session is running the
  /// message is queued (visible, editable) and fires when the turn completes —
  /// the queuing UX mirrors the TUI.
  Future<void> sendPrompt(String text) async {
    final s = current;
    if (s == null || text.trim().isEmpty) return;
    final composed = _withRemoteAttachments(text.trim());
    remotePending.clear();
    if (s.isRunning) {
      queue.add(composed);
      notifyListeners();
      return;
    }
    try {
      await _client.prompt(s.id, composed);
      await _refreshCurrent();
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
  }

  /// Fires the head of the queue against the current session once it is idle.
  Future<void> _drainQueue() async {
    if (queue.isEmpty) return;
    final s = current;
    if (s == null || s.isRunning) return;
    final next = queue.removeAt(0);
    notifyListeners();
    await sendPrompt(next);
  }

  /// Moves queued item [i] to the front and interrupts the running turn so it
  /// runs next ("run now"). Mirrors the TUI's promote behaviour.
  Future<void> promoteRemote(int i) async {
    if (i < 0 || i >= queue.length) return;
    if (i != 0) {
      final t = queue.removeAt(i);
      queue.insert(0, t);
    }
    notifyListeners();
    final s = current;
    if (s != null && s.isRunning) {
      await interrupt(); // the drain on the done/error event runs the head next
    } else {
      await _drainQueue();
    }
  }

  Future<void> interrupt() async {
    final s = current;
    if (s == null) return;
    try {
      await _client.interrupt(s.id);
    } catch (_) {}
  }

  void detach() {
    _sub?.cancel();
    _sub = null;
    current = null;
    events.clear();
    queue.clear();
  }

  @override
  void dispose() {
    _sub?.cancel();
    _directSub?.cancel();
    super.dispose();
  }
}
