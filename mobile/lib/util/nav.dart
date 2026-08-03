import 'package:flutter/material.dart';

import '../main.dart';
import '../widgets/readable.dart';

/// Leaves the current page: back if there is somewhere to go back to, home
/// when there is not.
///
/// A bare Navigator.pop assumes the page was pushed onto something, and the
/// sidebar bug proved the assumption can quietly fail — a stray pop had
/// removed the Home route, so every page-level back button on that stack led
/// to a white screen. The drawer is fixed, but a back button whose failure
/// mode is "the app is gone" deserves a floor: if the stack is empty, rebuild
/// it with Home at the bottom instead of popping into nothing.
/// Opens a conversation screen the way the current layout wants it opened.
///
/// On a phone, screens stack: list, then conversation, and back walks it.
/// On a desktop width the conversation screens carry the sidebar with them,
/// and stacking one conversation on another builds a back-history nobody
/// wanted — moving between recents should move sideways, not deeper. So a
/// wide layout replaces the top conversation instead of piling on it, except
/// at the root, where there is nothing to replace.
void openConversation(BuildContext context, Widget page) {
  final nav = Navigator.of(context);
  final route = MaterialPageRoute<void>(builder: (_) => page);
  if (isWide(context) && nav.canPop()) {
    nav.pushReplacement(route);
  } else {
    nav.push(route);
  }
}

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
