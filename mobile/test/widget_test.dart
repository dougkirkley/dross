import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:holocron/app_state.dart';
import 'package:holocron/main.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('direct mode is the default home and shows the Chats list',
      (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider(
        create: (_) => AppState(), // defaults to direct mode
        child: const HolocronApp(),
      ),
    );
    await tester.pump();

    expect(find.text('Chats'), findsOneWidget);
    expect(find.text('New chat'), findsOneWidget);
    expect(find.text('cost-aware coding, in your pocket'), findsOneWidget);
  });
}
