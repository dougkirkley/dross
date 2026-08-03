import 'package:flutter_test/flutter_test.dart';
import 'package:dross/models.dart';
import 'package:dross/screens/chat_screen.dart';

void main() {
  Event ev(int seq, String kind, [String data = '']) =>
      Event(seq: seq, kind: kind, data: data);

  test('sources render after the turn they fed, even when it failed', () {
    // The failure shape from the field: retrieval settles and emits sources,
    // then the model errors. Shown in arrival order that read as ten sources
    // followed by the failure they never fed.
    final msgs = chatMessages([
      ev(1, 'prompt', 'best mattresses'),
      ev(2, 'sources', '[]'),
      ev(3, 'error', 'model refused'),
    ]);
    expect(msgs.map((e) => e.kind).toList(), ['prompt', 'error', 'sources']);
  });

  test('sources stay under their own turn when another follows', () {
    final msgs = chatMessages([
      ev(1, 'prompt', 'first'),
      ev(2, 'sources', '[]'),
      ev(3, 'text', 'answer one'),
      ev(4, 'prompt', 'second'),
      ev(5, 'text', 'answer two'),
    ]);
    expect(msgs.map((e) => e.kind).toList(),
        ['prompt', 'text', 'sources', 'prompt', 'text']);
  });

  test('the happy path is unchanged: text then sources', () {
    final msgs = chatMessages([
      ev(1, 'prompt', 'q'),
      ev(2, 'text', 'a'),
      ev(3, 'sources', '[]'),
    ]);
    expect(msgs.map((e) => e.kind).toList(), ['prompt', 'text', 'sources']);
  });
}
