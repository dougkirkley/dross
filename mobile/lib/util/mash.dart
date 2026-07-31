/// Keyboard mash detection: catching "a cat walked across the keyboard" input
/// before it is sent as a prompt, so a stray elbow doesn't burn a model turn.
///
/// The bar is deliberately high: a false positive blocks real work, while a
/// false negative only costs what it costs today. Anything containing a
/// plausible word, a path, a flag, or code punctuation is left alone.
/// Mirrors internal/tui/mash.go — keep the two in step.
library;

const _rows = ['qwertyuiop', 'asdfghjkl', 'zxcvbnm'];

final Map<String, List<int>> _keyPos = () {
  final m = <String, List<int>>{};
  for (var r = 0; r < _rows.length; r++) {
    for (var c = 0; c < _rows[r].length; c++) {
      m[_rows[r][c]] = [r, c];
    }
  }
  return m;
}();

/// Longest run of letters stepping one key at a time in a single direction
/// along one keyboard row ("asdfg", "qwerty", "fdsa"). A loose "keys are near
/// each other" test flags real words (rhythm, strength); a straight drag does not.
int _longestRowRun(String s) {
  var best = 1, run = 1, dir = 0;
  for (var i = 1; i < s.length; i++) {
    final a = _keyPos[s[i - 1]], b = _keyPos[s[i]];
    var step = 0;
    if (a != null && b != null && a[0] == b[0]) step = b[1] - a[1];
    if (step == 1 || step == -1) {
      if (step == dir) {
        run++;
      } else {
        run = 2;
        dir = step;
      }
      if (run > best) best = run;
    } else {
      run = 1;
      dir = 0;
    }
  }
  return best;
}

bool _tokenIsMash(String w) {
  final lw = w.toLowerCase();
  if (!RegExp(r'^[a-z]+$').hasMatch(lw)) return false; // punctuation: intentional
  if (lw.length < 4) return false; // "the", "fix", "go" are real often enough

  // A long run of one character ("aaaaaaa").
  var run = 1, maxRun = 1;
  for (var i = 1; i < lw.length; i++) {
    if (lw[i] == lw[i - 1]) {
      run++;
      if (run > maxRun) maxRun = run;
    } else {
      run = 1;
    }
  }
  if (maxRun >= 4) return true;

  // No vowel at all in a long token ("sdfghjkl").
  if (lw.length >= 5 && !RegExp(r'[aeiouy]').hasMatch(lw)) return true;

  // A hand dragged straight along a key row.
  return _longestRowRun(lw) >= 4;
}

/// Whether [s] looks like accidental keyboard input rather than a real prompt.
bool looksLikeMash(String s) {
  final t = s.trim();
  if (t.length < 6) return false; // short input is usually deliberate
  // Code, paths, flags, commands: real input.
  if (RegExp('[/\\\\.:_=<>(){}\\[\\]\$#@|*"\'`-]').hasMatch(t)) return false;
  if (RegExp(r'[0-9]').hasMatch(t)) return false; // digits are rare in a mash
  final fields = t.split(RegExp(r'\s+')).where((w) => w.isNotEmpty).toList();
  if (fields.isEmpty || fields.length > 4) return false; // a sentence
  return fields.every(_tokenIsMash);
}
