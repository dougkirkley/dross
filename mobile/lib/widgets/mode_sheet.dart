import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../models.dart';
import '../theme.dart';

/// The "Select mode" bottom sheet — Claude's permission-mode picker: Manual /
/// Accept edits / Plan / Auto, each with an icon, description, and a check on
/// the active mode.
Future<void> showModeSheet(BuildContext context) {
  final state = context.read<AppState>();
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    builder: (_) => _ModeSheet(state: state),
  );
}

class _ModeSheet extends StatefulWidget {
  const _ModeSheet({required this.state});
  final AppState state;
  @override
  State<_ModeSheet> createState() => _ModeSheetState();
}

class _ModeSheetState extends State<_ModeSheet> {
  static const _rows = [
    (PermissionMode.manual, Icons.back_hand_outlined, T.text,
        'Always ask before making changes'),
    (PermissionMode.acceptEdits, Icons.code, Color(0xFFA78BFA),
        'Automatically accept all file edits'),
    (PermissionMode.plan, Icons.description_outlined, Color(0xFF6EA8FE),
        'Create a plan before making changes'),
    (PermissionMode.auto, Icons.bolt, Color(0xFFD98A63),
        'Ergon handles permission decisions'),
  ];

  @override
  Widget build(BuildContext context) {
    final current = widget.state.permissionMode;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                CircleIconButton(
                    icon: Icons.close, onTap: () => Navigator.pop(context)),
                const Expanded(
                    child: Text('Select mode',
                        style: T.title, textAlign: TextAlign.center)),
                const SizedBox(width: 44),
              ],
            ),
            const SizedBox(height: 16),
            Container(
              decoration: BoxDecoration(
                color: T.surface,
                borderRadius: BorderRadius.circular(T.rCard),
                border: Border.all(color: T.border),
              ),
              child: Column(
                children: [
                  for (var i = 0; i < _rows.length; i++) ...[
                    _row(_rows[i], current),
                    if (i != _rows.length - 1)
                      const Divider(height: 1, color: T.borderFaint, indent: 60),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _row((PermissionMode, IconData, Color, String) r, PermissionMode cur) {
    final (mode, icon, color, desc) = r;
    return InkWell(
      onTap: () {
        widget.state.setPermissionMode(mode);
        Navigator.pop(context);
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
        child: Row(
          children: [
            Icon(icon, color: color, size: 24),
            const SizedBox(width: 16),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(mode.label, style: T.rowTitle),
                  const SizedBox(height: 2),
                  Text(desc, style: T.meta),
                ],
              ),
            ),
            if (mode == cur)
              const Icon(Icons.check, color: T.accent, size: 22),
          ],
        ),
      ),
    );
  }
}
