import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// Rename a session, from wherever it was asked for.
///
/// The id is untouched: it is what the relay device, the workload label and
/// every stored event key on, so a rename must not be able to move a session
/// out from under them. This is a label for a human.
///
/// Shared, because there are three places to ask now — the list card's swipe
/// actions, the chat list, and the session page's own menu — and a rename that
/// looked different depending on where it was reached from would read as three
/// different features.
Future<void> showRenameDialog(BuildContext context, Session sess) async {
  final ctrl = TextEditingController(text: sess.title);
  final name = await showDialog<String>(
    context: context,
    builder: (dctx) => AlertDialog(
      backgroundColor: T.surface,
      title: const Text('Rename', style: T.title),
      content: TextField(
        controller: ctrl,
        autofocus: true,
        style: T.body,
        decoration: const InputDecoration(
          hintText: 'a name you will recognise',
          border: OutlineInputBorder(),
        ),
        onSubmitted: (v) => Navigator.pop(dctx, v),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(dctx),
            child: const Text('Cancel', style: TextStyle(color: T.textDim))),
        TextButton(
            onPressed: () => Navigator.pop(dctx, ctrl.text),
            child: const Text('Rename', style: TextStyle(color: T.accent))),
      ],
    ),
  );
  ctrl.dispose();
  if (name == null || !context.mounted) return;
  await context.read<AppState>().renameSession(sess.id, name);
}
