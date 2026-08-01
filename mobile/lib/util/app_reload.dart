/// Reloads the app itself (not just its data).
///
/// On web this clears the PWA's service-worker cache so a new build is actually
/// picked up; on native it does nothing, since the binary can't reload itself.
library;

export 'app_reload_stub.dart' if (dart.library.html) 'app_reload_web.dart';
