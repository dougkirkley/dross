import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';
import 'util/http_client.dart';

class ApiException implements Exception {
  final int status;
  final String body;
  ApiException(this.status, this.body);
  @override
  String toString() => 'server $status: $body';
}

/// DrossClient talks to `dross serve`. Sessions are server-side, so a run
/// continues whether or not the app is attached: fire an async prompt, then tail
/// the SSE stream, reconnecting from the last seq for lossless catch-up.
class DrossClient {
  DrossClient({required this.baseUrl, this.token});

  final String baseUrl; // e.g. https://dross.example.com
  final String? token;

  // On web this sends the same-origin SSO cookie (withCredentials); on native
  // it's a plain client and auth travels as the bearer token.
  final http.Client _http = createHttpClient();

  Uri _uri(String path) => Uri.parse('$baseUrl/$path');

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (token != null && token!.isNotEmpty)
          'Authorization': 'Bearer $token',
      };

  void _check(http.Response r) {
    if (r.statusCode < 200 || r.statusCode >= 300) {
      throw ApiException(r.statusCode, r.body);
    }
  }

  // --- sessions ---------------------------------------------------------

  Future<List<Session>> listSessions() async {
    final r = await _http.get(_uri('api/sessions'), headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['sessions'] as List? ?? []);
    return list
        .map((e) => Session.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<Session> createSession(
      {String? model,
      String? workerModel,
      String? style,
      bool chat = false,
      String? forge,
      String? repo,
      String? branch}) async {
    final body = jsonEncode({
      if (model != null) 'model': model,
      if (workerModel != null) 'worker_model': workerModel,
      if (style != null) 'style': style,
      if (chat) 'chat': true,
      // The host and the "owner/name" on it, never a URL. The server looks the
      // repository up on the host, so what the app sends cannot decide where a
      // credential is sent.
      if (forge != null) 'forge': forge,
      if (repo != null) 'repo': repo,
      if (branch != null && branch.isNotEmpty) 'branch': branch,
    });
    final r =
        await _http.post(_uri('api/sessions'), headers: _headers, body: body);
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  Future<Session> getSession(String id) async {
    final r = await _http.get(_uri('api/sessions/$id'), headers: _headers);
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  /// Uploads a file into the server workspace (multipart field "asset") and
  /// returns its workspace-relative path (e.g. uploads/foo.png).
  Future<String> upload(List<int> bytes, String filename) async {
    final req = http.MultipartRequest('POST', _uri('api/upload'));
    if (token != null && token!.isNotEmpty) {
      req.headers['Authorization'] = 'Bearer $token';
    }
    req.files
        .add(http.MultipartFile.fromBytes('asset', bytes, filename: filename));
    final resp = await http.Response.fromStream(await _http.send(req));
    _check(resp);
    final body = jsonDecode(resp.body) as Map<String, dynamic>;
    return body['path'] as String? ?? '';
  }

  Future<void> deleteSession(String id) async {
    final r = await _http.delete(_uri('api/sessions/$id'), headers: _headers);
    _check(r);
  }

  Future<Session> updateSessionModel(String id, String model) async {
    final r = await _http.patch(_uri('api/sessions/$id'),
        headers: _headers, body: jsonEncode({'model': model}));
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  /// Rename a session. Only the display name changes; the id the relay, the
  /// workload label and the stored events all key on is untouched.
  Future<Session> renameSession(String id, String title) async {
    final r = await _http.patch(_uri('api/sessions/$id'),
        headers: _headers, body: jsonEncode({'title': title}));
    _check(r);
    return Session.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  /// Models the server offers for a picker, and which of them are loaded.
  ///
  /// [chat] asks for a chat session's list, which differs when the server
  /// answers chat on its own backend.
  Future<ModelChoices> serverModels({bool chat = false}) async {
    final r = await _http.get(_uri('api/models${chat ? '?chat=1' : ''}'),
        headers: _headers);
    _check(r);
    return ModelChoices.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  Future<void> prompt(String id, String text) async {
    final r = await _http.post(_uri('api/sessions/$id/prompt'),
        headers: _headers, body: jsonEncode({'prompt': text}));
    _check(r);
  }

  /// Ends the server-side session. The response is a redirect to the identity
  /// provider's logout; we only care that the server saw the request.
  Future<void> logout() async {
    await _http.post(_uri('auth/logout'), headers: _headers);
  }

  Future<void> interrupt(String id) async {
    final r =
        await _http.post(_uri('api/sessions/$id/interrupt'), headers: _headers);
    _check(r);
  }

  /// Answer a permission gate a linked machine is blocked on. The ask id pins
  /// the reply to the question that raised it, so an answer sent late lands
  /// nowhere rather than approving whatever is asked next.
  Future<void> answer(String id, String askId, String answer) async {
    final r = await _http.post(
      _uri('api/sessions/$id/answer'),
      headers: {..._headers, 'Content-Type': 'application/json'},
      body: jsonEncode({'ask_id': askId, 'answer': answer}),
    );
    _check(r);
  }

  Future<List<Event>> events(String id, {int after = 0}) async {
    final r = await _http.get(_uri('api/sessions/$id/events?after=$after'),
        headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['events'] as List? ?? []);
    return list.map((e) => Event.fromJson(e as Map<String, dynamic>)).toList();
  }

  // --- git hosts --------------------------------------------------------

  /// The hosts this server can start sessions from, and which of them this
  /// account has connected.
  Future<List<ForgeHost>> forges() async {
    final r = await _http.get(_uri('api/forges'), headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['hosts'] as List? ?? []);
    return list
        .map((e) => ForgeHost.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// The repositories one connected host can see.
  ///
  /// A 403 here means the credential is gone or has expired past renewing,
  /// which is the same thing to a person: connect again.
  Future<RepoList> forgeRepos(String host) async {
    final r =
        await _http.get(_uri('api/forges/$host/repos'), headers: _headers);
    _check(r);
    return RepoList.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  /// Forget this account's credential for one host.
  Future<void> disconnectForge(String host) async {
    final r = await _http.delete(_uri('api/forges/$host'), headers: _headers);
    _check(r);
  }

  /// Where to send a browser to connect a host.
  ///
  /// A URL rather than a request: the authorization code has to reach the
  /// server and not this app, so the whole exchange happens in a browser that
  /// the server redirects. What comes back here is the answer to [forges].
  Uri connectUrl(String host, {String next = '/'}) =>
      _uri('api/forges/$host/connect?next=${Uri.encodeQueryComponent(next)}');

  // --- accounts ---------------------------------------------------------

  /// Whether this server still has nobody who can approve anyone. Answerable
  /// before approval, because that is exactly the state it reports.
  Future<bool> needsSetup() async {
    final r = await _http.get(_uri('api/setup/status'), headers: _headers);
    _check(r);
    return (jsonDecode(r.body)['needs_setup'] as bool?) ?? false;
  }

  /// Takes the administrator role. Throws if someone else already has it.
  Future<void> claimAdmin() async {
    final r = await _http.post(_uri('api/setup/claim'), headers: _headers);
    _check(r);
  }

  /// Whether the signed-in identity may administer accounts. The server
  /// answers this, not the client: the app must never decide for itself that a
  /// screen is allowed, it only decides whether to bother offering it.
  Future<bool> isAdmin() async {
    final r = await _http.get(_uri('api/whoami'), headers: _headers);
    _check(r);
    return (jsonDecode(r.body)['admin'] as bool?) ?? false;
  }

  /// Every registered account, whoever is waiting first.
  Future<List<Account>> accounts() async {
    final r = await _http.get(_uri('api/admin/users'), headers: _headers);
    _check(r);
    final list = (jsonDecode(r.body)['users'] as List? ?? []);
    return list
        .map((e) => Account.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Remove an account entirely. Signing in again makes them a new pending
  /// stranger, which is what separates this from denying them.
  Future<void> removeAccount(String email) async {
    final r =
        await _http.delete(_uri('api/admin/users/$email'), headers: _headers);
    _check(r);
  }

  /// Promote or demote. The server refuses to demote the last administrator.
  Future<void> setRole(String email, String role) async {
    final r = await _http.post(_uri('api/admin/users/$email/role'),
        headers: _headers, body: jsonEncode({'role': role}));
    _check(r);
  }

  /// Approve or deny one account. Returns the welcome mail's error, if the
  /// decision landed but telling them did not — that is worth surfacing without
  /// implying the approval failed.
  Future<String?> decideAccount(String email, String action) async {
    final r = await _http.post(_uri('api/admin/users/$email/decide'),
        headers: _headers, body: jsonEncode({'action': action}));
    _check(r);
    return jsonDecode(r.body)['mail_error'] as String?;
  }

  Future<PublishResult> publish(String path) async {
    final r = await _http.post(_uri('api/artifacts'),
        headers: _headers, body: jsonEncode({'path': path}));
    _check(r);
    return PublishResult.fromJson(jsonDecode(r.body) as Map<String, dynamic>);
  }

  // --- SSE stream -------------------------------------------------------

  /// streamEvents tails the live SSE stream from `after`. Yields each Event.
  /// The returned stream ends when the connection closes; callers reconnect
  /// with the last seq seen.
  Stream<Event> streamEvents(String id, {int after = 0}) async* {
    final req =
        http.Request('GET', _uri('api/sessions/$id/stream?after=$after'));
    req.headers.addAll({..._headers, 'Accept': 'text/event-stream'});
    final resp = await _http.send(req);
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw ApiException(resp.statusCode, '');
    }
    final lines =
        resp.stream.transform(utf8.decoder).transform(const LineSplitter());
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
