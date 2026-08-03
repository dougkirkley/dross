import 'package:flutter/material.dart';

import '../theme.dart';

/// Keeps a transcript pinned to the newest line, but only while the reader is
/// already down there.
///
/// An answer arrives a word at a time, so the transcript rebuilds continuously
/// for as long as one is being written. Scrolling to the end on every one of
/// those rebuilds means that scrolling up to re-read something drags you back
/// down before you can finish the sentence — on a phone, where a screen holds
/// three paragraphs, that makes a running turn unreadable. So follow from the
/// bottom, let go the moment somebody scrolls away from it, and offer a way
/// back rather than taking it for them.
class FollowBottom extends ChangeNotifier {
  FollowBottom() {
    controller.addListener(_onScroll);
  }

  /// How far off the end still counts as being at the end. A line of text
  /// landing under your thumb must not read as walking away.
  static const _slack = 80.0;

  /// Past this, going back to the newest line is a jump rather than a journey
  /// through everything in between.
  static const _tooFarToAnimate = 900.0;

  final ScrollController controller = ScrollController();

  bool _following = true;

  /// Whether the view is currently tracking the end of the content.
  bool get following => _following;

  /// Scrolls the view we drive ourselves, which the listener must not read as
  /// the reader moving. Counted, not flagged: one scroll can be cut short by
  /// the next, and its future still completes.
  int _driving = 0;

  void _onScroll() {
    if (_driving > 0 || !controller.hasClients) return;
    final p = controller.position;
    final atEnd = p.maxScrollExtent - p.pixels <= _slack;
    if (atEnd == _following) return;
    _following = atEnd;
    notifyListeners();
  }

  /// Call from `build`. Moves to the end after this frame if we're following,
  /// and does nothing at all if we're not.
  void follow() {
    if (!_following) return;
    WidgetsBinding.instance.addPostFrameCallback((_) => _toEnd());
  }

  /// Go back to the newest line and resume following it — for when the reader
  /// asks (the jump button) or says something themselves, which is a request to
  /// watch the reply.
  void pin() {
    if (!_following) {
      _following = true;
      notifyListeners();
    }
    // Now, not after the next frame: this is called from a tap, when the
    // layout is already settled and nothing else is due to draw. Waiting for a
    // frame that nobody has scheduled means waiting forever.
    _toEnd();
  }

  void _toEnd() {
    if (!controller.hasClients || !_following) return;
    final p = controller.position;
    final gap = p.maxScrollExtent - p.pixels;
    if (gap <= 1) return;
    if (gap > _tooFarToAnimate) {
      _driving++;
      controller.jumpTo(p.maxScrollExtent);
      _driving--;
      return;
    }
    _driving++;
    controller
        .animateTo(p.maxScrollExtent,
            duration: const Duration(milliseconds: 180), curve: Curves.easeOut)
        .whenComplete(() => _driving--);
  }

  @override
  void dispose() {
    controller.removeListener(_onScroll);
    controller.dispose();
    super.dispose();
  }
}

/// The way back down, shown between the transcript and the composer while the
/// reader is somewhere above the end of it.
///
/// It takes no room at all when it isn't offered, and it sits clear of the send
/// button, so the thumb reaching for it can't post half a sentence.
class JumpToLatest extends StatelessWidget {
  const JumpToLatest({super.key, required this.follow, this.label = 'Latest'});

  final FollowBottom follow;

  /// What the button says it will take you to.
  final String label;

  @override
  Widget build(BuildContext context) {
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 160),
      child: follow.following
          ? const SizedBox.shrink()
          : Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: Material(
                color: T.surface,
                elevation: 6,
                shadowColor: Colors.black54,
                borderRadius: BorderRadius.circular(T.rPill),
                child: InkWell(
                  borderRadius: BorderRadius.circular(T.rPill),
                  onTap: follow.pin,
                  child: Container(
                    padding: const EdgeInsets.fromLTRB(12, 7, 14, 7),
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(T.rPill),
                      border: Border.all(color: T.border),
                    ),
                    child: Row(mainAxisSize: MainAxisSize.min, children: [
                      const Icon(Icons.arrow_downward,
                          size: 15, color: T.textDim),
                      const SizedBox(width: 6),
                      Text(label,
                          style: const TextStyle(
                              color: T.textDim,
                              fontSize: 13,
                              fontWeight: FontWeight.w600)),
                    ]),
                  ),
                ),
              ),
            ),
    );
  }
}
