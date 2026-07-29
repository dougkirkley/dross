import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:ergon/app_state.dart';
import 'package:ergon/main.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('direct mode is the default home and shows the Chats list',
      (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider(
        create: (_) => AppState(), // defaults to direct mode
        child: const ErgonApp(),
      ),
    );
    await tester.pump();

    expect(find.text('Chats'), findsOneWidget);
    expect(find.text('New chat'), findsOneWidget);
    expect(find.text('Cost-aware coding,\nin your pocket.'), findsOneWidget);
  });
}
