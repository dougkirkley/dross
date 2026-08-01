import 'dart:js_interop';

/// Web implementation: real navigation and cache teardown.
///
/// Interop note: these must be called as *bound* methods. Declaring
/// `@JS('location.assign')` as a top-level external pulls the function off its
/// object, and invoking it detached throws "Illegal invocation" in every
/// browser — which silently broke sign-out. Reach the objects first, then call
/// methods on them.
@JS('location')
external _Location get _location;

@JS('navigator')
external _Navigator get _navigator;

@JS('caches')
external JSAny? get _caches;

extension type _Location(JSObject _) implements JSObject {
  external void reload();
  external void assign(String url);
  external set href(String v);
}

extension type _Navigator(JSObject _) implements JSObject {
  external _ServiceWorkerContainer? get serviceWorker;
}

extension type _ServiceWorkerContainer(JSObject _) implements JSObject {
  external JSPromise<JSArray<JSObject>> getRegistrations();
}

extension type _Registration(JSObject _) implements JSObject {
  external JSPromise<JSBoolean> unregister();
}

extension type _CacheStorage(JSObject _) implements JSObject {
  external JSPromise<JSArray<JSString>> keys();
  external JSPromise<JSBoolean> delete(String key);
}

/// Reloads the app, dropping any service worker and its caches first so a new
/// build is actually fetched rather than replayed from cache.
Future<void> reloadApp() async {
  try {
    final sw = _navigator.serviceWorker;
    if (sw != null) {
      final regs = await sw.getRegistrations().toDart;
      for (final r in regs.toDart) {
        await _Registration(r).unregister().toDart;
      }
    }
  } catch (_) {
    // No service worker, or the browser refused: the reload below still helps.
  }
  try {
    final c = _caches;
    if (c != null) {
      final cs = _CacheStorage(c as JSObject);
      final keys = await cs.keys().toDart;
      for (final k in keys.toDart) {
        await cs.delete(k.toDart).toDart;
      }
    }
  } catch (_) {
    // Cache Storage unavailable; fall through to the reload.
  }
  _location.reload();
}

/// Leaves the app for [url] as a real document navigation, so the server sees
/// the request and any Set-Cookie it returns takes effect. Used for sign-out,
/// which must clear the server session rather than only local state.
Future<void> navigateTo(String url) async {
  try {
    _location.assign(url);
  } catch (_) {
    // Fall back to href: a navigation that fails silently means the user is
    // left signed in, which is worse than any cosmetic difference.
    _location.href = url;
  }
}
