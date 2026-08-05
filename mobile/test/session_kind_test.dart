import 'package:flutter_test/flutter_test.dart';
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
}
