import 'package:flutter_test/flutter_test.dart';

import 'package:dross/models.dart';
import 'package:dross/widgets/activity.dart';

Event ev(String kind, [String data = '']) =>
    Event(seq: 1, kind: kind, data: data);

void main() {
  _inFlight();
  _liveSetup();
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

// The starting panel and the transcript were saying the same sentence, one
// directly under the other.
void _liveSetup() {
  test('the line the panel is showing is not repeated below it', () {
    final events = [
      ev('meta', "Cloning into 'dross'..."),
      ev('prompt', 'hello'),
    ];
    final shown = withoutLiveSetup(events, "Cloning into 'dross'...");
    expect(shown.map((e) => e.kind), ['prompt']);
  });

  test('once setup is done the record is whole again', () {
    final events = [
      ev('meta', "Cloning into 'dross'..."),
      ev('meta', "Switched to a new branch 'dross/x'"),
    ];
    expect(withoutLiveSetup(events, '').length, 2);
  });
}

// A tool is listed the moment it is called, and its result arrives later — so
// while a turn is running the newest step and the live row underneath were the
// same command, once in the past tense before it had happened.
void _inFlight() {
  const tool = '{"name":"bash","input":{"command":"go test ./..."}}';

  test('the tool still running is not also listed as done', () {
    final timeline = buildTimeline([ev('tool', tool)], running: true);
    expect(timeline.whereType<Activity>(), isEmpty,
        reason: 'the only step is the one the running row is showing');
  });

  test('it joins the list once its result lands', () {
    final timeline = buildTimeline([ev('tool', tool), ev('tool-result', 'ok')],
        running: true);
    expect(timeline.whereType<Activity>().single.steps, hasLength(1));
  });

  test('a finished turn lists everything, result or not', () {
    // A run that ended on an error or a cancel never gets its result event.
    // Hiding the step then would lose it for good.
    final timeline = buildTimeline([ev('tool', tool)], running: false);
    expect(timeline.whereType<Activity>().single.steps, hasLength(1));
  });

  test('earlier steps stay while the newest one runs', () {
    final timeline = buildTimeline([
      ev('tool', tool),
      ev('tool-result', 'ok'),
      ev('tool', tool),
    ], running: true);
    expect(timeline.whereType<Activity>().single.steps, hasLength(1));
  });
}
