import 'package:flutter_test/flutter_test.dart';
import 'package:dross/models.dart';
import 'package:dross/widgets/activity.dart';

void main() {
  Event ev(String kind, String data) => Event(seq: 1, kind: kind, data: data);

  test('the spinner names the site being searched', () {
    expect(currentActivityLabel([ev('search', 'best mattresses reddit')]),
        'Searching reddit for "best mattresses"');
    expect(currentActivityLabel([ev('search', 'best mattresses wirecutter')]),
        'Searching wirecutter for "best mattresses"');
    // The plain query, with no site appended, reads as the open web.
    expect(currentActivityLabel([ev('search', 'best mattresses')]),
        'Searching the web for "best mattresses"');
  });

  test('the most recent event wins, so the label tracks progress', () {
    final label = currentActivityLabel([
      ev('prompt', 'best mattresses'),
      ev('search', 'best mattresses'),
      ev('search', 'best mattresses amazon'),
    ]);
    expect(label, 'Searching amazon for "best mattresses"');
  });
}
