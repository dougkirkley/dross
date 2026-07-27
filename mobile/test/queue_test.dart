import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:holocron/app_state.dart';
import 'package:holocron/direct/provider.dart';
import 'package:holocron/models.dart';

/// A provider whose reply stream is driven by the test. Each call to [stream]
/// creates a fresh controller exposed via [controllers].
class FakeProvider implements DirectProvider {
  final List<StreamController<String>> controllers = [];
  @override
  String get model => 'fake';
  @override
  Stream<String> stream(List<ChatMessage> history) {
    final c = StreamController<String>();
    controllers.add(c);
    return c.stream;
  }
}

Future<void> _tick() => Future<void>.delayed(Duration.zero);

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  AppState newState(FakeProvider fake) {
    final s = AppState();
    s.providerOverride = () async => fake;
    return s;
  }

  test('first send starts a streaming turn', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('hello');
    await _tick();

    expect(s.streaming, isTrue);
    expect(s.currentChat!.messages.length, 2); // user + streaming assistant
    fake.controllers.last.add('hi');
    await _tick();
    expect(s.currentChat!.messages.last.text, 'hi');
  });

  test('direct chat does not queue: a send while streaming is ignored',
      () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await s.sendDirect('two');
    await _tick();

    expect(s.queue, isEmpty); // direct mode never queues
    expect(fake.controllers.length, 1); // no second turn started
    expect(s.currentChat!.messages.length, 2); // still just turn one
  });

  test('a send after the turn completes starts a fresh turn', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await fake.controllers.first.close(); // finish turn one
    await _tick();
    expect(s.streaming, isFalse);

    await s.sendDirect('two');
    await _tick();
    expect(s.streaming, isTrue);
    expect(fake.controllers.length, 2);
  });

  test('removeQueued drops a message out and returns its text', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    // The queue is the remote-session queue; exercise it directly.
    s.queue.addAll(['two', 'three']);

    final dropped = s.removeQueued(0);
    expect(dropped, 'two');
    expect(s.queue, ['three']);
  });

  test('interruptDirect stops streaming', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    fake.controllers.first.add('partial');
    await _tick();

    s.interruptDirect();
    await _tick();

    expect(s.streaming, isFalse);
    expect(s.currentChat!.messages.last.text, 'partial');
  });
}
