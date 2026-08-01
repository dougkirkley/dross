import 'dart:js_interop';

/// Web implementation: a genuine app reload.
///
/// A plain page reload is not enough for an installed PWA — the Flutter service
/// worker keeps serving the cached bundle, so the user sees the old build no
/// matter how many times they pull to refresh. Unregister the worker and drop
/// its caches first, then reload so the fresh bundle is fetched from the server.
@JS('navigator.serviceWorker.getRegistrations')
external JSPromise<JSArray<JSObject>> _getRegistrations();

@JS('caches.keys')
external JSPromise<JSArray<JSString>> _cacheKeys();

@JS('caches.delete')
external JSPromise<JSBoolean> _cacheDelete(JSString key);

@JS('location.reload')
external void _reload();

@JS('location.assign')
external void _assign(JSString url);

extension type _Registration(JSObject _) implements JSObject {
  external JSPromise<JSBoolean> unregister();
}

Future<void> reloadApp() async {
  try {
    final regs = await _getRegistrations().toDart;
    for (final r in regs.toDart) {
      await _Registration(r).unregister().toDart;
    }
  } catch (_) {
    // No service worker (or the browser refuses): the reload below still helps.
  }
  try {
    final keys = await _cacheKeys().toDart;
    for (final k in keys.toDart) {
      await _cacheDelete(k).toDart;
    }
  } catch (_) {
    // Cache Storage unavailable; fall through to the reload.
  }
  _reload();
}

/// Leaves the app for [url] as a real document navigation, so the server sees
/// the request (and any Set-Cookie it returns takes effect). Used for sign-out,
/// which must clear the server session rather than only local state.
Future<void> navigateTo(String url) async {
  _assign(url.toJS);
}
