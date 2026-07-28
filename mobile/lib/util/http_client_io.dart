import 'package:http/http.dart' as http;

/// Native/default client (mobile, desktop). Auth travels as a bearer token.
http.Client createHttpClient() => http.Client();
