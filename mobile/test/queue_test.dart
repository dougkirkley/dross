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

  test('a send during streaming is queued, not sent', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await s.sendDirect('two');
    await _tick();

    expect(s.queue, ['two']);
    expect(fake.controllers.length, 1); // only the first turn started
  });

  test('queued message fires when the turn completes', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await s.sendDirect('two');
    await _tick();

    await fake.controllers.first.close(); // finish turn one
    await _tick();

    expect(s.queue, isEmpty);
    expect(fake.controllers.length, 2); // turn two auto-started
    expect(s.streaming, isTrue);
  });

  test('removeQueued drops a message out and returns its text', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await s.sendDirect('two');
    await s.sendDirect('three');
    await _tick();

    expect(s.queue, ['two', 'three']);
    final dropped = s.removeQueued(0);
    expect(dropped, 'two');
    expect(s.queue, ['three']);
  });

  test('promoteQueued interrupts the current turn and runs the queued one',
      () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    fake.controllers.first.add('partial reply');
    await _tick();
    await s.sendDirect('two');
    await _tick();

    s.promoteQueued(); // Ctrl+C behaviour
    await _tick();

    expect(s.queue, isEmpty);
    expect(fake.controllers.length, 2); // second turn started immediately
    // The interrupted assistant kept its partial text; a new user turn 'two'
    // and a fresh streaming assistant were appended.
    final msgs = s.currentChat!.messages;
    expect(msgs[1].text, 'partial reply');
    expect(msgs[1].streaming, isFalse);
    expect(msgs[2].role, 'user');
    expect(msgs[2].text, 'two');
    expect(msgs.last.streaming, isTrue);
  });

  test('interruptDirect stops streaming without firing the queue', () async {
    final fake = FakeProvider();
    final s = newState(fake);
    await s.sendDirect('one');
    await _tick();
    await s.sendDirect('two');
    await _tick();

    s.interruptDirect();
    await _tick();

    expect(s.streaming, isFalse);
    expect(s.queue, ['two']); // still pending, not auto-fired
    expect(fake.controllers.length, 1);
  });
}
