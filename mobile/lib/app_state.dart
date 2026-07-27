import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';
import 'models.dart';

/// AppState holds the server config and the currently-attached session's live
/// event stream. It survives view changes so a run keeps streaming while the
/// user navigates.
class AppState extends ChangeNotifier {
  String baseUrl = '';
  String token = '';

  List<Session> sessions = [];
  bool loadingSessions = false;
  String? error;

  // Currently-attached session.
  Session? current;
  final List<Event> events = [];
  StreamSubscription<Event>? _sub;

  bool get configured => baseUrl.isNotEmpty;

  HolocronClient get _client => HolocronClient(baseUrl: baseUrl, token: token);

  Future<void> load() async {
    final prefs = await SharedPreferences.getInstance();
    baseUrl = prefs.getString('baseUrl') ?? '';
    token = prefs.getString('token') ?? '';
    notifyListeners();
    if (configured) await refreshSessions();
  }

  Future<void> saveConfig(String url, String tok) async {
    baseUrl = url.trim().replaceAll(RegExp(r'/+$'), '');
    token = tok.trim();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('baseUrl', baseUrl);
    await prefs.setString('token', token);
    notifyListeners();
    if (configured) await refreshSessions();
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
      final s = await _client.createSession(model: model, workerModel: workerModel);
      await refreshSessions();
      return s;
    } catch (e) {
      error = e.toString();
      notifyListeners();
      return null;
    }
  }

  /// attach loads history for a session and starts tailing its live stream.
  Future<void> attach(Session s) async {
    await _sub?.cancel();
    current = s;
    events.clear();
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
          if (ev.kind == 'done' || ev.kind == 'error') _refreshCurrent();
          notifyListeners();
        }
      },
      onDone: () {
        // Stream closed (e.g. backgrounded). Reconnect from the last seq.
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
    } catch (_) {}
  }

  Future<void> sendPrompt(String text) async {
    final s = current;
    if (s == null || text.trim().isEmpty) return;
    try {
      await _client.prompt(s.id, text.trim());
      await _refreshCurrent();
      notifyListeners();
    } catch (e) {
      error = e.toString();
      notifyListeners();
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
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}
