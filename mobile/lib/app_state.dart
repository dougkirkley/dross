import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter_web_auth_2/flutter_web_auth_2.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';
import 'models.dart';
import 'util/file_pick.dart';
import 'util/app_reload.dart';
import 'util/mash.dart';

/// The active sidebar tab. Both tabs are backed by the same `dross serve`
/// sessions; only the presentation differs (chat = conversational, code = full
/// workspace UI).
enum AppMode { chat, code }

/// AppState is a thin client for `dross serve`. All agent/provider config lives
/// on the server — the app only needs the server URL and a Google SSO session.
/// The app has no local settings beyond the server URL.
class AppState extends ChangeNotifier {
  final _secure = const FlutterSecureStorage();
  // Cryptographically secure: this seeds the OAuth state.
  final _rng = Random.secure();

  String baseUrl = '';
  String token = '';

  /// Which sidebar tab is showing (Chat vs Code). UI-only. Chat is the landing
  /// tab: it's the lighter surface to open on, and code sessions are one tap away.
  AppMode mode = AppMode.chat;
  void setMode(AppMode m) {
    if (mode == m) return;
    mode = m;
    notifyListeners();
  }

  /// Ids of sessions last opened in Chat view. The server has one session type,
  /// so this local set is what lets the combined Recents list show a session as
  /// a chat vs code entry and reopen it in the same view. Persisted.
  final Set<String> _chatViews = {};

  /// The view a session was last opened in (defaults to code).
  AppMode viewOf(String id) =>
      _chatViews.contains(id) ? AppMode.chat : AppMode.code;

  /// Sessions last opened as chats, and everything else (code). The two tabs
  /// filter on these so a chat and a code session are distinct surfaces.
  List<Session> get chatSessions =>
      sessions.where((x) => viewOf(x.id) == AppMode.chat).toList();
  List<Session> get codeSessions =>
      sessions.where((x) => viewOf(x.id) == AppMode.code).toList();

  /// Records (and persists) which view a session was opened in.
  Future<void> markView(String id, AppMode m) async {
    final changed =
        m == AppMode.chat ? _chatViews.add(id) : _chatViews.remove(id);
    if (!changed) return;
    final p = await SharedPreferences.getInstance();
    await p.setStringList('chatViews', _chatViews.toList());
    notifyListeners();
  }

  List<Session> sessions = [];
  bool loadingSessions = false;

  /// True once the first session fetch has completed, so the lists show a
  /// loading screen (not the empty state) until sessions are actually available.
  bool sessionsLoaded = false;

  /// Ids whose server-side delete is in flight. A concurrent refreshSessions
  /// must not re-add these, or a just-deleted session flickers back until the
  /// DELETE lands.
  final Set<String> _deleting = {};
  Session? current;
  final List<Event> events = [];

  /// Messages typed while a turn is running; drained when it finishes.
  final List<String> queue = [];

  /// Files uploaded to the current session's workspace, awaiting the next prompt.
  final List<({String name, String path})> remotePending = [];

  StreamSubscription<Event>? _sub;
  String? error;

  bool get remoteConfigured => baseUrl.isNotEmpty;

  /// Set true by signOut so the app gates behind the sign-in screen even on web
  /// (where a same-origin cookie would otherwise keep `authed` true). Cleared on
  /// the next successful sign-in.
  bool _signedOut = false;

  /// Whether the user is authenticated. On web the same-origin session
  /// (Cloudflare Access / cookie) covers it; on native we need an SSO token.
  bool get authed => !_signedOut && (kIsWeb || token.isNotEmpty);

  DrossClient get _client => DrossClient(baseUrl: baseUrl, token: token);

  // --- load / server config -------------------------------------------

  Future<void> load() async {
    final p = await SharedPreferences.getInstance();
    baseUrl = p.getString('baseUrl') ?? '';
    // The web build is served by `dross serve`; default to its own origin.
    if (baseUrl.isEmpty && kIsWeb) baseUrl = defaultWebServer();
    _chatViews.addAll(p.getStringList('chatViews') ?? const []);
    await _loadRead();
    token = await _secure.read(key: 'remoteToken') ?? '';
    notifyListeners();
    if (remoteConfigured && authed) await refreshSessions();
  }

  /// Sets (or updates) the server URL — the app's only setting.
  Future<void> setServer(String url) async {
    url = url.trim();
    if (url.isEmpty || url == baseUrl) return;
    baseUrl = url;
    final p = await SharedPreferences.getInstance();
    await p.setString('baseUrl', baseUrl);
    notifyListeners();
    if (authed) await refreshSessions();
  }

  Future<void> signOut() async {
    _signedOut = true;
    token = '';
    await _secure.delete(key: 'remoteToken');
    detach();
    sessions = [];
    notifyListeners();
    // Clearing local state is not a sign-out: on web the session cookie (and
    // the identity provider's own session) would survive, so signing back in
    // returns instantly without authenticating. Hand off to the server's logout
    // endpoint, which clears the cookie and ends the upstream session.
    if (kIsWeb && baseUrl.isNotEmpty) {
      await navigateTo('$baseUrl/auth/logout');
      return;
    }
    // Native holds a bearer token rather than a cookie; dropping it above is
    // enough, but tell the server anyway so the session is not left dangling.
    try {
      await _client.logout();
    } catch (_) {
      // Offline sign-out still succeeds locally.
    }
  }

  /// Runs the server's Google SSO flow and stores the returned session token. On
  /// web the same-origin session cookie already covers API calls, so it just
  /// refreshes. Returns an error string, or null on success.
  Future<String?> signInRemote() async {
    if (baseUrl.isEmpty) return 'Set the server URL first';
    if (kIsWeb) {
      _signedOut = false;
      notifyListeners();
      await refreshSessions();
      return null;
    }
    try {
      // Bind the callback to this attempt. The dross:// scheme is BROWSABLE and
      // any installed app can claim it, so without a state to check, a hostile
      // caller could hand us a session token of its choosing and we would adopt
      // its account.
      final state = base64Url.encode(
          List<int>.generate(24, (_) => _rng.nextInt(256)));
      final result = await FlutterWebAuth2.authenticate(
        url: '$baseUrl/auth/login?mode=app&state=${Uri.encodeQueryComponent(state)}',
        callbackUrlScheme: 'dross',
      );
      final params = Uri.parse(result).queryParameters;
      if (params['state'] != state) return 'Sign-in rejected: state mismatch';
      final session = params['session'];
      if (session == null || session.isEmpty) return 'No session returned';
      token = session;
      _signedOut = false;
      await _secure.write(key: 'remoteToken', value: token);
      notifyListeners();
      await refreshSessions();
      return null;
    } catch (e) {
      return e.toString();
    }
  }

  /// Pull-to-refresh handler for list/entry screens.
  ///
  /// On web this reloads the app itself: an installed PWA keeps serving the
  /// cached bundle, so re-fetching data alone would never surface a new build —
  /// which is the whole reason to pull. On native there is no bundle to
  /// refetch, so it just re-reads state.
  Future<void> refreshApp() async {
    if (kIsWeb) {
      await reloadApp(); // clears the service-worker cache, then reloads
      return;
    }
    await load();
  }

  // --- sessions -------------------------------------------------------

  Future<void> refreshSessions() async {
    loadingSessions = true;
    error = null;
    notifyListeners();
    try {
      final fetched = await _client.listSessions();
      // Drop any session whose delete is still in flight so it doesn't flicker
      // back into the list mid-delete.
      sessions = fetched.where((x) => !_deleting.contains(x.id)).toList();
    } catch (e) {
      error = e.toString();
    } finally {
      loadingSessions = false;
      sessionsLoaded = true; // first fetch done; screens can leave the spinner
      notifyListeners();
    }
  }

  Future<Session?> createSession(
      {String? model, String? workerModel, bool chat = false}) async {
    try {
      final s = await _client.createSession(
          model: model, workerModel: workerModel, chat: chat);
      await refreshSessions();
      return s;
    } catch (e) {
      error = e.toString();
      notifyListeners();
      return null;
    }
  }

  Future<void> deleteSession(Session s) async {
    _deleting.add(s.id);
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
      await markView(s.id, AppMode.code); // forget any chat-view record
    } catch (e) {
      error = e.toString();
      // Server still has it (or we're offline): re-sync so the UI reflects
      // reality instead of silently dropping a session that wasn't deleted.
      _deleting.remove(s.id);
      await refreshSessions();
      return;
    }
    _deleting.remove(s.id);
  }

  /// Models the server offers, for the code-session model picker.
  Future<List<String>> availableServerModels() async {
    try {
      return await _client.serverModels();
    } catch (_) {
      return [];
    }
  }

  /// Changes the current session's model (applies to the next prompt).
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

  /// Renames a session for display. The id stays as it is, so nothing that
  /// keys on it — the relay device, the workload, the stored events — moves.
  Future<void> renameSession(String id, String title) async {
    try {
      final updated = await _client.renameSession(id, title.trim());
      if (current?.id == updated.id) current = updated;
      final i = sessions.indexWhere((x) => x.id == updated.id);
      if (i >= 0) sessions[i] = updated;
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
  }

  // --- attach / stream ------------------------------------------------

  /// When each session was last read, by id. Persisted, because an unread
  /// badge that forgets on restart tells you least at the moment you most want
  /// it: opening the app after being away.
  final Map<String, DateTime> _read = {};
  static const _readKey = 'sessionsRead';

  Future<void> _loadRead() async {
    final p = await SharedPreferences.getInstance();
    for (final entry in p.getStringList(_readKey) ?? const <String>[]) {
      final i = entry.indexOf('=');
      if (i <= 0) continue;
      final at = DateTime.tryParse(entry.substring(i + 1));
      if (at != null) _read[entry.substring(0, i)] = at;
    }
    notifyListeners();
  }

  Future<void> _saveRead() async {
    final p = await SharedPreferences.getInstance();
    await p.setStringList(_readKey,
        [for (final e in _read.entries) '${e.key}=${e.value.toIso8601String()}']);
  }

  /// Whether a session has produced something since it was last opened.
  ///
  /// Compared against the session's own updated time rather than counted from
  /// events, so it works for sessions the client has never streamed — which is
  /// most of them, most of the time.
  bool unread(Session s) {
    final at = s.updated;
    if (at == null) return false;
    if (current?.id == s.id) return false; // being read right now
    final seen = _read[s.id];
    return seen == null || at.isAfter(seen);
  }

  /// Marks a session read as of now.
  void markRead(Session s) {
    _read[s.id] = DateTime.now().toUtc();
    _saveRead();
    notifyListeners();
  }

  Future<void> attach(Session s) async {
    _sub?.cancel();
    _sub = null;
    current = s;
    markRead(s);
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

  /// Pull-to-refresh for a conversation: re-fetch the session and its full
  /// event log, then restart the stream. Cheaper than detach/attach because the
  /// screen keeps its scroll position while the list is rebuilt.
  Future<void> reloadCurrent() async {
    final s = current;
    if (s == null) return;
    try {
      final fresh = await _client.getSession(s.id);
      final evs = await _client.events(s.id);
      current = fresh;
      events
        ..clear()
        ..addAll(evs);
      notifyListeners();
      await _sub?.cancel();
      _sub = null;
      _startStream();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
  }

  Future<void> _refreshCurrent() async {
    final s = current;
    if (s == null) return;
    try {
      final fresh = await _client.getSession(s.id);
      current = fresh;
      // And the copy in the list, which is what the cards are built from.
      // Refreshing only `current` left the list holding the session as it was
      // when it was created — so a session titled from its first prompt still
      // read as untitled on the card behind it.
      final i = sessions.indexWhere((x) => x.id == fresh.id);
      if (i >= 0) sessions[i] = fresh;
      notifyListeners();
    } catch (_) {}
  }

  // --- prompts / queue ------------------------------------------------

  /// Set when a prompt looked like a keyboard mash; the UI shows it and the next
  /// send of the same text goes through.
  String? mashWarning;
  String _mashSeen = '';

  /// Sends [text]. Returns false when nothing was sent (empty, no session, or
  /// the keyboard-mash guard held it back) so callers can keep the composer text.
  Future<bool> sendPrompt(String text) async {
    final s = current;
    if (s == null || text.trim().isEmpty) return false;
    // Keyboard-mash guard: a cat on the keyboard shouldn't cost a model turn.
    // Warn once; sending the same text again goes through.
    if (looksLikeMash(text) && _mashSeen != text.trim()) {
      _mashSeen = text.trim();
      mashWarning =
          'That looks like a keyboard mash. Send again to run it anyway.';
      notifyListeners();
      return false;
    }
    mashWarning = null;
    _mashSeen = '';
    final composed = _withRemoteAttachments(text.trim());
    remotePending.clear();
    if (s.isRunning) {
      queue.add(composed);
      notifyListeners();
      return true;
    }
    try {
      await _client.prompt(s.id, composed);
      await _refreshCurrent();
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
    }
    return true;
  }

  Future<void> _drainQueue() async {
    if (queue.isEmpty) return;
    final s = current;
    if (s == null || s.isRunning) return;
    final next = queue.removeAt(0);
    notifyListeners();
    await sendPrompt(next);
  }

  /// Moves queued item [i] to the front so it is the next prompt to run. If the
  /// session is idle it starts immediately; otherwise it runs when the current
  /// turn finishes. Does not interrupt the running turn (use Stop for that).
  Future<void> promoteRemote(int i) async {
    if (i <= 0 || i >= queue.length) return; // already front or out of range
    final t = queue.removeAt(i);
    queue.insert(0, t);
    notifyListeners();
    await _drainQueue(); // no-op while running; sends immediately when idle
  }

  String removeQueued(int i) {
    if (i < 0 || i >= queue.length) return '';
    final s = queue.removeAt(i);
    notifyListeners();
    return s;
  }

  Future<void> interrupt() async {
    final s = current;
    if (s == null) return;
    try {
      await _client.interrupt(s.id);
    } catch (_) {}
  }

  /// Answer a permission gate raised by the machine running this session.
  ///
  /// Failures are surfaced rather than swallowed: the run is blocked until the
  /// answer arrives, so silently dropping one would leave it hanging with no
  /// sign of why.
  Future<String?> answerAsk(String askId, String answer) async {
    final s = current;
    if (s == null) return 'no session';
    try {
      await _client.answer(s.id, askId, answer);
      return null;
    } catch (e) {
      return e.toString();
    }
  }

  // --- attachments ----------------------------------------------------

  /// Uploads [f] into the server workspace and stages it for the next prompt.
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
    super.dispose();
  }
}

/// defaultWebServer returns the origin the web app was served from (its own
/// `dross serve`). Empty off web or for non-http origins.
String defaultWebServer() {
  try {
    final u = Uri.base;
    if (u.scheme == 'http' || u.scheme == 'https') {
      return u.origin;
    }
  } catch (_) {}
  return '';
}
