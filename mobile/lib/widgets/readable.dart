import 'package:flutter/material.dart';

import '../theme.dart';

/// Centres its child and stops it growing past a comfortable reading width.
///
/// Applied to the conversation and the composer together, so the text and the
/// box you type into stay in the same column rather than the input sprawling
/// under a narrow transcript.
class Readable extends StatelessWidget {
  const Readable({super.key, required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) => Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: T.readWidth),
          child: child,
        ),
      );
}
