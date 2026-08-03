import 'package:flutter_test/flutter_test.dart';
import 'package:dross/app_state.dart';
import 'package:dross/models.dart';

void main() {
  Session sess(String id) =>
      Session(id: id, model: 'm', provider: 'p', status: 'idle', cost: 0, events: 0);

  test('a fetch still carrying a deleted session does not resurrect it', () {
    final deleted = {'b'};
    // The poll that was already in flight when the delete landed.
    final visible = visibleSessions([sess('a'), sess('b')], deleted);
    expect(visible.map((x) => x.id), ['a']);
    expect(deleted, {'b'}, reason: 'still gone as far as the server knows');
  });

  test('the tombstone lifts once the server stops returning it', () {
    final deleted = {'b'};
    final visible = visibleSessions([sess('a')], deleted);
    expect(visible.map((x) => x.id), ['a']);
    expect(deleted, isEmpty);
  });

  // The case that broke: deleting the only session. Nothing else in the list
  // masks the row coming back, so it sits there looking like a dead button.
  test('deleting the last session leaves the list empty', () {
    final deleted = {'only'};
    expect(visibleSessions([sess('only')], deleted), isEmpty);
    expect(visibleSessions([], deleted), isEmpty);
    expect(deleted, isEmpty);
  });

  test('a session deleted elsewhere and recreated is not hidden forever', () {
    final deleted = {'b'};
    visibleSessions([sess('a')], deleted); // server confirms it is gone
    final visible = visibleSessions([sess('a'), sess('b')], deleted);
    expect(visible.map((x) => x.id), ['a', 'b']);
  });
}
