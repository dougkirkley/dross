import 'package:fetch_client/fetch_client.dart';
import 'package:http/http.dart' as http;

/// Web client backed by the fetch API so response bodies (the live SSE stream)
/// arrive incrementally. The XHR-based BrowserClient buffers the whole response
/// until the request completes, which stalled live streaming — events only
/// appeared once the connection closed. Credentials are included so the
/// same-origin SSO session cookie is sent with /api requests.
http.Client createHttpClient() => FetchClient(
      mode: RequestMode.cors,
      // `.cors` maps to fetch's 'include' — send credentials (the SSO cookie)
      // on every request, matching the old BrowserClient withCredentials=true.
      credentials: RequestCredentials.cors,
    );
