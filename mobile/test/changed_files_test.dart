import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:dross/models.dart';
import 'package:dross/widgets/changed_files.dart';

int _seq = 0;
Event edit(String file, {int added = 0, int removed = 0, bool isNew = false}) =>
    Event(
      seq: _seq++,
      kind: 'edit',
      data: jsonEncode({
        'file': file,
        'added': added,
        'removed': removed,
        'new': isNew,
      }),
    );

Event other(String kind, String data) =>
    Event(seq: _seq++, kind: kind, data: data);

void main() {
  // The whole session's work on a file, not the last edit's. "What has this
  // changed" is a question about the session, and a file touched four times
  // answering with the fourth touch is answering a different one.
  test('edits to one file accumulate into a single entry', () {
    final files = changedFiles([
      edit('pkg/agent/agent.go', added: 10, removed: 2),
      other('tool', 'read'),
      edit('pkg/agent/agent.go', added: 5, removed: 1),
    ]);
    expect(files.length, 1);
    expect(files.single.added, 15);
    expect(files.single.removed, 3);
  });

  // First-touch order, so the list does not reshuffle under you while you read
  // it — the same rule the terminal's /files uses.
  test('files stay in the order they were first touched', () {
    final files = changedFiles([
      edit('b.go', added: 1),
      edit('a.go', added: 1),
      edit('b.go', added: 1),
    ]);
    expect(files.map((f) => f.path), ['b.go', 'a.go']);
  });

  test('a created file is marked as one', () {
    final files = changedFiles([edit('new.go', added: 40, isNew: true)]);
    expect(files.single.created, isTrue);
  });

  // Malformed payloads have reached this stream before. A count is not worth
  // taking the screen down for.
  test('unparseable edits are skipped, not fatal', () {
    final files = changedFiles([
      other('edit', 'not json at all'),
      edit('', added: 3), // no path: nothing to list
      edit('real.go', added: 1),
    ]);
    expect(files.map((f) => f.path), ['real.go']);
  });

  testWidgets('the chip totals every file', (tester) async {
    final files = changedFiles([
      edit('a.go', added: 10, removed: 2),
      edit('b.go', added: 5),
    ]);
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: Center(child: ChangedFilesChip(files: files)))));
    expect(find.text('2 files'), findsOneWidget);
    expect(find.text('+15'), findsOneWidget);
    expect(find.text('−2'), findsOneWidget);
  });

  // Nothing touched, nothing shown: an empty "0 files +0" is a row of chrome
  // reporting that there is nothing to report.
  testWidgets('nothing is drawn when nothing has changed', (tester) async {
    await tester.pumpWidget(const MaterialApp(
        home: Scaffold(body: Center(child: ChangedFilesChip(files: [])))));
    expect(find.byType(Text), findsNothing);
  });
}
