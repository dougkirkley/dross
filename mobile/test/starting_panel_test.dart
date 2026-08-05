import 'package:flutter_test/flutter_test.dart';

import 'package:dross/models.dart';
import 'package:dross/screens/session_screen.dart';

Event ev(String kind) => Event(seq: 1, kind: kind, data: '');

void main() {
  test('a session that has never said anything is starting, not restarting',
      () {
    expect(isResuming([]), isFalse);
  });

  // The regression: a session started on a repository says it is cloning before
  // its pod has ever run. Counting that as "has said something" told somebody
  // their files were being brought back when there were not yet any files.
  test('setup lines are not the session having said something', () {
    expect(isResuming([ev('meta')]), isFalse);
    expect(isResuming([ev('meta'), ev('meta')]), isFalse);
  });

  test('a session with real history is coming back', () {
    expect(isResuming([ev('meta'), ev('prompt')]), isTrue);
    expect(isResuming([ev('text')]), isTrue);
  });
}
