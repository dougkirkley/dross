// Returns an http.Client. On web it enables credentials so the same-origin
// SSO session cookie is sent with /api requests (BrowserClient defaults to
// withCredentials=false, which would 401 every call behind auth).
export 'http_client_io.dart' if (dart.library.html) 'http_client_web.dart';
