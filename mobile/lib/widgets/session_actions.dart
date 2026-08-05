import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';
import 'rename_dialog.dart';
import 'swipe_actions.dart';

/// What can be done to a session from a list.
///
/// One list, because there are three places that show sessions — the code list,
/// the chat list, and the drawer's recents — and a session that could be
/// renamed in two of them and deleted in one would read as three features
/// rather than one.
List<SwipeAction> sessionActions(BuildContext context, Session sess) => [
      SwipeAction(
        icon: Icons.drive_file_rename_outline,
        label: 'Rename',
        onTap: () => showRenameDialog(context, sess),
      ),
      SwipeAction(
        icon: Icons.delete_outline,
        label: 'Delete',
        color: T.danger,
        iconColor: T.bg,
        destructive: true,
        onTap: () => context.read<AppState>().deleteSession(sess),
      ),
    ];
