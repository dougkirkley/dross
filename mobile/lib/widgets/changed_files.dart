import 'package:flutter/material.dart';

import '../models.dart';
import '../theme.dart';
import 'modal.dart';

/// One file a session has touched, with its running diffstat.
class ChangedFile {
  ChangedFile(
      {required this.path,
      required this.added,
      required this.removed,
      required this.created});
  final String path;
  int added;
  int removed;
  final bool created;

  String get name {
    final i = path.lastIndexOf('/');
    return i < 0 ? path : path.substring(i + 1);
  }

  String get dir {
    final i = path.lastIndexOf('/');
    return i < 0 ? '' : path.substring(0, i);
  }
}

/// Folds a session's edit events into the list of files it has changed.
///
/// Accumulated rather than replaced: a file edited four times is one entry
/// whose counts are the whole session's work on it, which is the question
/// "what has this changed" actually asks. Kept in first-touch order, because
/// that is the order the work happened in and it does not reshuffle under you
/// while you read it. Same rule as the terminal's, deliberately — the two are
/// often open on the same session and must not disagree about the count.
List<ChangedFile> changedFiles(List<Event> events) {
  final out = <ChangedFile>[];
  final byPath = <String, ChangedFile>{};
  for (final e in events) {
    if (e.baseKind != 'edit') continue;
    final info = e.editInfo;
    if (info == null || info.file.isEmpty) continue;
    final seen = byPath[info.file];
    if (seen != null) {
      seen.added += info.added;
      seen.removed += info.removed;
      continue;
    }
    final f = ChangedFile(
        path: info.file,
        added: info.added,
        removed: info.removed,
        created: info.isNew);
    byPath[info.file] = f;
    out.add(f);
  }
  return out;
}

/// "3 files +81 −12" — the one fact worth carrying continuously.
///
/// A count and a total, not a list: the header has room for a fact, and the
/// list is one tap away. It is how much is uncommitted that you want to see
/// without asking, rather than the detail you want occasionally.
class ChangedFilesChip extends StatelessWidget {
  const ChangedFilesChip({super.key, required this.files, this.onTap});
  final List<ChangedFile> files;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    if (files.isEmpty) return const SizedBox.shrink();
    var added = 0, removed = 0;
    for (final f in files) {
      added += f.added;
      removed += f.removed;
    }
    return OutlineChip(
      onTap: onTap,
      child: Row(mainAxisSize: MainAxisSize.min, children: [
        Text('${files.length} file${files.length == 1 ? '' : 's'}',
            style: T.meta),
        const SizedBox(width: 8),
        Text('+$added', style: T.meta.copyWith(color: T.ok)),
        if (removed > 0) ...[
          const SizedBox(width: 5),
          // U+2212, not a hyphen: it is the same width as the plus beside it,
          // so the two numbers do not sit on a ragged edge.
          Text('−$removed', style: T.meta.copyWith(color: T.danger)),
        ],
      ]),
    );
  }
}

/// The list behind the chip: every file the session has touched, in the order
/// it first touched them.
Future<void> showChangedFilesSheet(
        BuildContext context, List<ChangedFile> files) =>
    showResponsiveModal<void>(
      context: context,
      maxWidth: 560,
      builder: (_, dialog) => _Sheet(files: files, dialog: dialog),
    );

class _Sheet extends StatelessWidget {
  const _Sheet({required this.files, this.dialog = false});
  final List<ChangedFile> files;
  final bool dialog;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        constraints:
            BoxConstraints(maxHeight: MediaQuery.of(context).size.height * 0.8),
        decoration: modalDecoration(dialog),
        child: ListView(
          padding: const EdgeInsets.all(16),
          shrinkWrap: true,
          children: [
            Row(children: [
              CircleIconButton(
                  icon: Icons.close, onTap: () => Navigator.pop(context)),
              const Expanded(
                  child: Text('Changed files',
                      style: T.title, textAlign: TextAlign.center)),
              const SizedBox(width: 44),
            ]),
            const SizedBox(height: 8),
            for (final f in files) _row(f),
          ],
        ),
      ),
    );
  }

  Widget _row(ChangedFile f) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 9),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Icon(f.created ? Icons.note_add_outlined : Icons.edit_outlined,
              size: 17, color: T.textDim),
          const SizedBox(width: 12),
          Expanded(
            child:
                Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(f.name,
                  style: T.body, maxLines: 1, overflow: TextOverflow.ellipsis),
              // The directory under the name rather than beside it: a path is
              // long and the number on the right is the thing being compared
              // down the column, so it must not be pushed off the edge.
              if (f.dir.isNotEmpty)
                Text(f.dir,
                    style: T.metaFaint,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
            ]),
          ),
          const SizedBox(width: 12),
          Text('+${f.added}', style: T.mono.copyWith(color: T.ok)),
          if (f.removed > 0) ...[
            const SizedBox(width: 6),
            Text('−${f.removed}', style: T.mono.copyWith(color: T.danger)),
          ],
        ]),
      );
}
