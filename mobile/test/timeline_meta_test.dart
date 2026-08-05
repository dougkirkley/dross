import 'package:flutter_test/flutter_test.dart';

import 'package:dross/models.dart';
import 'package:dross/widgets/activity.dart';

Event ev(String kind, [String data = '']) =>
    Event(seq: 1, kind: kind, data: data);

void main() {
  // The line saying a session is cloning used to be folded into an Activity and
  // drawn under a collapsed "Used a tool" — which is exactly where nobody looks
  // for the answer to "is this doing anything?".
  test('setup lines are not folded into a tool group', () {
    final timeline = buildTimeline([ev('meta', "Cloning into 'dross'...")]);
    expect(timeline, hasLength(1));
    expect(timeline.first, isA<Event>(),
        reason: 'an Activity here means it is hidden behind "Used a tool"');
  });

  test('a setup line breaks a run of tool steps rather than joining it', () {
    final timeline = buildTimeline([
      ev('tool', '{"name":"read","input":{}}'),
      ev('meta', "Cloning into 'dross'..."),
      ev('tool', '{"name":"read","input":{}}'),
    ]);
    expect(timeline.whereType<Event>().length, 1);
    expect(timeline.whereType<Activity>().length, 2,
        reason: 'the tool steps either side stay their own groups');
  });
}
