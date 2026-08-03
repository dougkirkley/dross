import 'package:flutter/material.dart';

import '../theme.dart';
import 'readable.dart';

/// Shows [child] the way the current width wants it shown: a bottom sheet on a
/// phone, a centered dialog on a desktop.
///
/// The sheet is a thumb idiom — it exists because that is where a thumb is. A
/// pointer is wherever the control that opened it was, and a panel sliding up
/// from the foot of a large window is nowhere near it.
///
/// [builder] receives whether it is being shown as a dialog, because the shape
/// differs: a dialog is rounded and bordered all round, where a sheet is docked
/// to an edge and only needs the top. Nothing else about the content changes,
/// which is the point of having one function.
Future<R?> showResponsiveModal<R>({
  required BuildContext context,
  required Widget Function(BuildContext context, bool dialog) builder,
  double maxWidth = 480,
}) {
  if (isWide(context)) {
    return showDialog<R>(
      context: context,
      builder: (ctx) => Dialog(
        backgroundColor: Colors.transparent,
        child: ConstrainedBox(
          constraints: BoxConstraints(maxWidth: maxWidth),
          child: builder(ctx, true),
        ),
      ),
    );
  }
  return showModalBottomSheet<R>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Padding(
      // Lift clear of the keyboard: a sheet with a text field in it is
      // otherwise typed at from underneath.
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
      child: builder(ctx, false),
    ),
  );
}

/// The panel shape a responsive modal wears: all corners on a dialog, top two
/// on a sheet.
BoxDecoration modalDecoration(bool dialog) => dialog
    ? BoxDecoration(
        color: T.surface,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: T.border),
      )
    : const BoxDecoration(
        color: T.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        border: Border(top: BorderSide(color: T.border)),
      );
