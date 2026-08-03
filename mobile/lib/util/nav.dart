import 'package:flutter/material.dart';

import '../main.dart';

/// Leaves the current page: back if there is somewhere to go back to, home
/// when there is not.
///
/// A bare Navigator.pop assumes the page was pushed onto something, and the
/// sidebar bug proved the assumption can quietly fail — a stray pop had
/// removed the Home route, so every page-level back button on that stack led
/// to a white screen. The drawer is fixed, but a back button whose failure
/// mode is "the app is gone" deserves a floor: if the stack is empty, rebuild
/// it with Home at the bottom instead of popping into nothing.
void backOrHome(BuildContext context) {
  final nav = Navigator.of(context);
  if (nav.canPop()) {
    nav.pop();
    return;
  }
  nav.pushAndRemoveUntil(
    MaterialPageRoute(builder: (_) => const Home()),
    (_) => false,
  );
}
