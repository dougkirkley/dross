// Cross-platform file picking. On web we use a real <input type=file> clicked
// synchronously inside the tap gesture (mobile Safari blocks a file dialog that
// isn't opened directly from a user gesture, which is why file_picker's async
// path silently did nothing). Native platforms keep file_picker.
import 'file_pick_io.dart' if (dart.library.html) 'file_pick_web.dart' as impl;

class PickedFile {
  PickedFile({required this.name, required this.bytes});
  final String name;
  final List<int> bytes;
}

/// Opens the platform file picker and returns the chosen file, or null if the
/// user cancelled.
Future<PickedFile?> pickFile() => impl.pickFileImpl();
