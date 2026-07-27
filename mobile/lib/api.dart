import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';

class ApiException implements Exception {
  final int status;
  final String body;
  ApiException(this.status, this.body);
  @override
  String toString() => 'server $status: $body';
}

/// HolocronClient talks to `holocron serve`. Sessions are server-side, so a run
/// continues whether or not the app is attached: fire an async prompt, then tail
/// the SSE stream, reconnecting from the last seq for lossless catch-up.
class HolocronClient {
  HolocronClient({required this.baseUrl, this.token});

  final String baseUrl; // e.g. https://holocron.example.com
  final String? token;

  Uri _uri(String path) => Uri.parse('$baseUrl/$path');

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (token != null && token!.isNotEmpty) 'Authorization': 'Bearer $token',
      };

  void _check(http.Response r) {
    if (r.statusCode < 200 || r.statusCode >= 300) {
      throw ApiException(r.statusCode, r.body);
    }
  }

  // --- sessions ---------------------------------------------------------

  Future<List<Session>> listSessions() async {
    final r = await http.get(_uri('api/sessions'), headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['sessions'] as List? ?? []);
    return list.map((e) => Session.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<Session> createSession({String? model, String? workerModel, String? style}) async {
    final body = jsonEncode({
      if (model != null) 'model': model,
      if (workerModel != null) 'worker_model': workerModel,
      if (style != null) 'style': style,
    });
    final r = await http.post(_uri('api/sessions'), headers: _headers, body: body);
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  Future<Session> getSession(String id) async {
    final r = await http.get(_uri('api/sessions/$id'), headers: _headers);
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  Future<void> deleteSession(String id) async {
    final r = await http.delete(_uri('api/sessions/$id'), headers: _headers);
    _check(r);
  }

  Future<void> prompt(String id, String text) async {
    final r = await http.post(_uri('api/sessions/$id/prompt'),
        headers: _headers, body: jsonEncode({'prompt': text}));
    _check(r);
  }

  Future<void> interrupt(String id) async {
    final r = await http.post(_uri('api/sessions/$id/interrupt'), headers: _headers);
    _check(r);
  }

  Future<List<Event>> events(String id, {int after = 0}) async {
    final r = await http.get(_uri('api/sessions/$id/events?after=$after'), headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['events'] as List? ?? []);
    return list.map((e) => Event.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<PublishResult> publish(String path) async {
    final r = await http.post(_uri('api/artifacts'),
        headers: _headers, body: jsonEncode({'path': path}));
    _check(r);
    return PublishResult.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  // --- SSE stream -------------------------------------------------------

  /// streamEvents tails the live SSE stream from `after`. Yields each Event.
  /// The returned stream ends when the connection closes; callers reconnect
  /// with the last seq seen.
  Stream<Event> streamEvents(String id, {int after = 0}) async* {
    final req = http.Request('GET', _uri('api/sessions/$id/stream?after=$after'));
    req.headers.addAll({..._headers, 'Accept': 'text/event-stream'});
    final resp = await http.Client().send(req);
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw ApiException(resp.statusCode, '');
    }
    final lines = resp.stream.transform(utf8.decoder).transform(const LineSplitter());
    await for (final line in lines) {
      if (!line.startsWith('data:')) continue;
      final payload = line.substring('data:'.length).trim();
      if (payload.isEmpty) continue;
      try {
        yield Event.fromJson(jsonDecode(payload) as Map<String, dynamic>);
      } catch (_) {
        // ignore non-event data lines
      }
    }
  }
}
