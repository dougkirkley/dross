/// Native implementation: there is no bundle to re-fetch, so a reload is a
/// no-op and callers fall back to refreshing data.
Future<void> reloadApp() async {}

/// Native has no document to navigate; callers hit the endpoint over HTTP.
Future<void> navigateTo(String url) async {}
