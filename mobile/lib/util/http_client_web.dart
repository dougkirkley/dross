import 'package:http/http.dart' as http;
import 'package:http/browser_client.dart';

/// Web client with credentials enabled so the same-origin SSO session cookie
/// is included on /api requests.
http.Client createHttpClient() => BrowserClient()..withCredentials = true;
