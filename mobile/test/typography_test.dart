import 'package:flutter_test/flutter_test.dart';
import 'package:dross/theme.dart';

void main() {
  // A monospaced face gives every letter the same width, so word shapes
  // flatten and long prose is markedly harder to read. The desktop transcript
  // was set in one to match the TUI; the resemblance was not worth the cost.
  test('transcript prose is never monospaced', () {
    for (final s in [T.prose, T.termProse, T.body, T.termBody, T.meta, T.termMeta]) {
      expect(s.fontFamily, isNot(T.monoFamily),
          reason: 'transcript text should use a reading face');
    }
  });

  // Code is the opposite case: character alignment is the whole point.
  test('code keeps a monospaced face', () {
    expect(T.mono.fontFamily, T.monoFamily);
  });

  // Dim text still has to be legible against a near-black background at 13px.
  test('dim transcript text is not the faintest grey', () {
    expect(T.termMeta.color, T.textDim);
    expect(T.termMeta.color, isNot(T.textFaint));
  });
}
