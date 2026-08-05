import 'package:flutter/material.dart';

import '../theme.dart';

/// Centres its child and stops it growing past a comfortable reading width.
///
/// Applied to the conversation and the composer together, so the text and the
/// box you type into stay in the same column rather than the input sprawling
/// under a narrow transcript.
class Readable extends StatelessWidget {
  const Readable({super.key, required this.child, this.maxWidth = T.readWidth});
  final Widget child;

  /// Defaults to reading width. The composer passes something wider: the cap
  /// exists so prose does not run in unreadably long lines, which is a fact
  /// about text being read rather than about a box being typed into.
  final double maxWidth;

  @override
  Widget build(BuildContext context) => Center(
        child: ConstrainedBox(
          constraints: BoxConstraints(maxWidth: maxWidth),
          child: child,
        ),
      );
}

/// Whether to use the desktop treatment: the transcript laid out like the
/// terminal rather than as chat bubbles.
///
/// Measured, not assumed — a narrow browser window gets the touch layout, and a
/// tablet in landscape gets the desktop one.
bool isWide(BuildContext context) =>
    MediaQuery.of(context).size.width >= T.wideAt;

// Transcript type, chosen by the width it will be read at. The desktop
// transcript matches the terminal; the phone one reads like a message thread.
TextStyle proseFor(BuildContext c) => isWide(c) ? T.termProse : T.prose;
TextStyle bodyFor(BuildContext c) => isWide(c) ? T.termBody : T.body;
TextStyle metaFor(BuildContext c) => isWide(c) ? T.termMeta : T.meta;
