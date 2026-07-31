import 'package:flutter_test/flutter_test.dart';

import 'package:dross/util/mash.dart';

/// Mirrors internal/tui/mash_test.go — the two heuristics must agree.
void main() {
  test('flags keyboard mashes', () {
    for (final s in [
      'asdfghjkl',
      'aaaaaaaaaa',
      'sdfghjk',
      'qwertyui',
      'jkljkljkl',
      'zxcvbnm',
      'hjkkkkkk',
      'fdsafdsa',
    ]) {
      expect(looksLikeMash(s), isTrue, reason: s);
    }
  });

  test('never blocks real prompts', () {
    for (final s in [
      'hi',
      'go',
      'fix the auth guard',
      'run the tests and fix any failures',
      'grep for TODO in internal/tui',
      'why does the build fail?',
      'refactor internal/cli/tui.go',
      'add a --json flag',
      'bump to 0.5.31 and deploy',
      'rhythm',
      'deploy',
      'status',
      'explain this stack trace',
      'what changed in the last commit',
      'schema',
      'strength',
    ]) {
      expect(looksLikeMash(s), isFalse, reason: s);
    }
  });
}
