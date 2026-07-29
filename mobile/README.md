# Ergon mobile

Flutter client for `ergon serve` — one codebase for **iOS and Android**. A
thin client over the server's HTTP + SSE API: create sessions, fire async
prompts, tail the live event stream (with lossless reconnect), interrupt, and
see cost. Dark theme, Carolina-blue accent, matching the TUI.

Flutter was chosen over native Swift specifically because Android is a goal: the
client is thin and API-driven, so a single Dart codebase ships to both stores.

## Structure

```
lib/
  models.dart         wire models (Session, Event, PublishResult)
  api.dart            ErgonClient — REST + SSE (streamEvents)
  app_state.dart      ChangeNotifier: config, sessions, attached-session stream
  main.dart           app + theme
  screens/
    sessions_screen.dart   session list + create
    session_screen.dart    live event stream + input + cost/interrupt
    settings_screen.dart   server URL + bearer token
pubspec.yaml          deps: http, provider, shared_preferences
```

The `lib/` code is complete. The platform runner directories (`ios/`,
`android/`, etc.) are **generated** by Flutter — see setup.

## Setup

Requires the Flutter SDK (https://docs.flutter.dev/get-started/install). Android
builds need only the Android SDK; iOS builds need Xcode.

```bash
cd mobile
flutter create --platforms=ios,android --org com.dougkirkley .   # generate runners, keep lib/
flutter pub get
flutter run                                                       # pick a device/simulator
```

`flutter create .` writes the platform scaffolding around the existing `lib/`
and `pubspec.yaml` without overwriting them.

Then open the app, go to **Settings**, and enter your `ergon serve` URL
(e.g. `https://ergon.example.com`) and an API token if auth is enabled
(a static token, or an OIDC/SSO bearer). Sessions and prompts flow from there.

## Notes

- **Remote control:** runs continue server-side; the app fires an async prompt
  then tails SSE. Backgrounding drops the stream; on foreground it reconnects
  from the last seq, so no events are missed.
- **Auth:** the bearer token goes in the `Authorization` header. Behind
  Cloudflare Access, front the server host with Access and let it inject the
  identity; the app still sends its token for the second layer.
- **Not yet built here:** authored against the server API but not compiled — no
  Flutter SDK on the dev machine that produced it. `flutter analyze` after
  `pub get` is the first check.
- **Roadmap:** push notifications for permission asks / idle / errors (APNs +
  FCM); inline artifact rendering (`GET /a/<id>`); file upload from the share
  sheet.
