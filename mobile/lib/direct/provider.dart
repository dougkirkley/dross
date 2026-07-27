import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models.dart';

/// A direct-mode backend: given the conversation so far, stream the assistant's
/// reply as text deltas. Chat/plan only — no tools (the phone has no repo, and
/// iOS forbids process exec). Full tool-driven coding lives in remote mode.
abstract class DirectProvider {
  String get model;

  /// Streams assistant text deltas for a reply to [history]. Throws
  /// [DirectError] on a non-2xx response.
  Stream<String> stream(List<ChatMessage> history);
}

class DirectError implements Exception {
  DirectError(this.status, this.body);
  final int status;
  final String body;
  @override
  String toString() => 'provider $status: $body';
}

Future<http.StreamedResponse> _send(
    Uri uri, Map<String, String> headers, Object body) async {
  final req = http.Request('POST', uri)
    ..headers.addAll(headers)
    ..body = jsonEncode(body);
  final resp = await http.Client().send(req);
  if (resp.statusCode < 200 || resp.statusCode >= 300) {
    final text = await resp.stream.bytesToString();
    throw DirectError(resp.statusCode, text);
  }
  return resp;
}

/// Yields the `data:` payloads of an SSE body, one per event, skipping blanks
/// and `[DONE]`.
Stream<String> _sseData(http.StreamedResponse resp) async* {
  final lines =
      resp.stream.transform(utf8.decoder).transform(const LineSplitter());
  await for (final line in lines) {
    if (!line.startsWith('data:')) continue;
    final payload = line.substring('data:'.length).trim();
    if (payload.isEmpty || payload == '[DONE]') continue;
    yield payload;
  }
}

/// Any OpenAI-compatible `/chat/completions` endpoint: OpenAI itself, or a local
/// ollama server (point [baseUrl] at its `/v1`). This is the free local tier.
class OpenAICompatProvider implements DirectProvider {
  OpenAICompatProvider({
    required this.baseUrl,
    required this.apiKey,
    required this.model,
    this.system = '',
  });

  final String baseUrl; // e.g. https://ollama.example.com/v1
  final String apiKey;
  @override
  final String model;
  final String system;

  @override
  Stream<String> stream(List<ChatMessage> history) async* {
    final messages = <Map<String, String>>[
      if (system.isNotEmpty) {'role': 'system', 'content': system},
      for (final m in history) {'role': m.role, 'content': m.text},
    ];
    final resp = await _send(
      Uri.parse('${baseUrl.replaceAll(RegExp(r'/+$'), '')}/chat/completions'),
      {
        'Content-Type': 'application/json',
        if (apiKey.isNotEmpty) 'Authorization': 'Bearer $apiKey',
      },
      {'model': model, 'stream': true, 'messages': messages},
    );
    await for (final payload in _sseData(resp)) {
      try {
        final j = jsonDecode(payload) as Map<String, dynamic>;
        final choices = j['choices'] as List?;
        if (choices == null || choices.isEmpty) continue;
        final delta = (choices.first as Map)['delta'] as Map?;
        final content = delta?['content'];
        if (content is String && content.isNotEmpty) yield content;
      } catch (_) {
        // ignore keepalive / non-JSON lines
      }
    }
  }
}

/// Anthropic Messages API, streaming, authenticated with an API key. For a
/// non-Anthropic Anthropic-compatible gateway, point [baseUrl] at it.
class AnthropicProvider implements DirectProvider {
  AnthropicProvider({
    required this.model,
    required this.apiKey,
    this.identity = '',
    this.baseUrl = 'https://api.anthropic.com',
    this.maxTokens = 4096,
  });

  @override
  final String model;
  final String apiKey;
  final String identity;
  final String baseUrl;
  final int maxTokens;

  Map<String, String> _headers() => {
        'Content-Type': 'application/json',
        'anthropic-version': '2023-06-01',
        'x-api-key': apiKey,
      };

  List<Map<String, String>> _system() => [
        if (identity.isNotEmpty) {'type': 'text', 'text': identity},
      ];

  @override
  Stream<String> stream(List<ChatMessage> history) async* {
    final messages = [
      for (final m in history)
        {
          'role': m.role,
          'content': [
            {'type': 'text', 'text': m.text}
          ],
        }
    ];
    final sys = _system();
    final resp = await _send(
      Uri.parse(
          '${baseUrl.replaceAll(RegExp(r'/+$'), '')}/v1/messages'),
      _headers(),
      {
        'model': model,
        'max_tokens': maxTokens,
        'stream': true,
        if (sys.isNotEmpty) 'system': sys,
        'messages': messages,
      },
    );
    await for (final payload in _sseData(resp)) {
      try {
        final j = jsonDecode(payload) as Map<String, dynamic>;
        if (j['type'] != 'content_block_delta') continue;
        final delta = j['delta'] as Map?;
        if (delta?['type'] == 'text_delta') {
          final t = delta?['text'];
          if (t is String && t.isNotEmpty) yield t;
        }
      } catch (_) {
        // ignore ping / non-JSON events
      }
    }
  }
}
