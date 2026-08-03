import 'package:flutter_test/flutter_test.dart';
import 'package:dross/models.dart';

void main() {
  test('parses what the server sent', () {
    final list = Source.listFrom(
        '[{"title":"Best mattresses","url":"https://example.com/m","site":"example.com","snippet":"picks"},'
        '{"url":"https://other.example/guide","site":"other.example"}]');
    expect(list.length, 2);
    expect(list[0].label, 'Best mattresses');
    // No title: fall back to the site rather than showing a bare URL.
    expect(list[1].label, 'other.example');
  });

  test('a broken list costs the answer nothing', () {
    // Malformed, wrong shape, and entries with no URL: all yield nothing rather
    // than throwing in the middle of rendering a reply.
    expect(Source.listFrom('not json'), isEmpty);
    expect(Source.listFrom('{"not":"a list"}'), isEmpty);
    expect(Source.listFrom('[{"title":"no url"}]'), isEmpty);
    expect(Source.listFrom(''), isEmpty);
  });
}
