import 'package:flutter_test/flutter_test.dart';
import 'package:dross/app_state.dart';
import 'package:dross/models.dart';

Session s({required String id, String device = '', bool connected = false}) =>
    Session(
      id: id,
      model: 'm',
      provider: 'p',
      status: 'idle',
      cost: 0,
      events: 0,
      device: device,
      connected: connected,
    );

void main() {
  // Both attach over the same relay and both carry a device, so `device` alone
  // says only "not in this process" — which is the thing that made a laptop and
  // a pod look identical in the list.
  test('a pod is named after the session it serves', () {
    final pod = s(id: 'ses_abc', device: 'ses_abc');
    expect(pod.inPod, isTrue);
    expect(pod.onLinkedMachine, isFalse);
  });

  test('a linked machine is named after the machine', () {
    final mac = s(id: 'ses_Mac.spaceballs-nmjyhab5', device: 'Mac.spaceballs');
    expect(mac.onLinkedMachine, isTrue);
    expect(mac.inPod, isFalse);
  });

  // A chat session runs in the server process and is neither.
  test('a session with no device is neither', () {
    final chat = s(id: 'ses_abc');
    expect(chat.onLinkedMachine, isFalse);
    expect(chat.inPod, isFalse);
  });

  _anyStartingTests();
}

// What keeps the list polling.
//
// A closed laptop is a device that is not connected, same as a pod still
// coming up — but nothing is on its way to fixing it. Counting it as
// "starting" left the list asking the server every three seconds for as long
// as that machine stayed off, blinking a spinner beside it each time.
void _anyStartingTests() {
  test('a pod still coming up keeps the list looking', () {
    final st = AppState();
    st.sessions = [s(id: 'ses_abc', device: 'ses_abc')];
    expect(st.anyStarting, isTrue);
  });

  test('a closed laptop does not', () {
    final st = AppState();
    st.sessions = [s(id: 'ses_Mac-1', device: 'Mac')];
    expect(st.anyStarting, isFalse);
  });

  test('a connected pod does not', () {
    final st = AppState();
    st.sessions = [s(id: 'ses_abc', device: 'ses_abc', connected: true)];
    expect(st.anyStarting, isFalse);
  });
}
