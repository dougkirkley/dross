import 'package:flutter_test/flutter_test.dart';
import 'package:dross/app_state.dart';
import 'package:dross/models.dart';

void main() {
  _queueArrow();
  _outOfOrder();
  Session sess(String id) => Session(
      id: id, model: 'm', provider: 'p', status: 'idle', cost: 0, events: 0);

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

// The list polls on a timer, so two fetches are routinely in flight and they do
// not come back in the order they were sent. A poll sent before a delete and
// answered after it still carries the deleted session — and by then the
// tombstone has been cleared by the fetch that came back in between, so there
// is nothing left to catch it. That is "I deleted it and it did not disappear".
void _outOfOrder() {
  test(
      'a fetch that started before the delete cannot be caught by the tombstone',
      () {
    Session sess(String id) => Session(
        id: id, model: 'm', provider: 'p', status: 'idle', cost: 0, events: 0);
    final deleted = {'b'};

    // The fetch sent after the delete comes back first: correct, and it clears
    // the tombstone because the server agrees the session is gone.
    final fresh = visibleSessions([sess('a')], deleted);
    expect(fresh.map((x) => x.id), ['a']);
    expect(deleted, isEmpty);

    // The older one lands afterwards, still carrying 'b'. Nothing about the
    // list itself can tell it is stale, which is why the ordering is decided by
    // the sequence number in refreshSessions rather than here.
    final stale = visibleSessions([sess('a'), sess('b')], deleted);
    expect(stale.map((x) => x.id), ['a', 'b'],
        reason: 'the tombstone alone cannot catch an out-of-order answer');
  });
}

// The arrow beside a queued message moves it to the front. Everything at the
// front is already there, so the control has to say so rather than absorb the
// tap — which is what "it does nothing" looked like with one queued message.
void _queueArrow() {
  test('the front of the queue cannot be moved further forward', () {
    final s = AppState();
    s.queue.addAll(['first']);
    expect(s.canPromote(0), isFalse);

    s.queue.add('second');
    expect(s.canPromote(1), isTrue);
    expect(s.canPromote(0), isFalse);
    expect(s.canPromote(2), isFalse, reason: 'past the end');
  });
}
