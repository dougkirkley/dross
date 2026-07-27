import 'package:file_picker/file_picker.dart';

import 'file_pick.dart';

/// Native file pick via file_picker.
Future<PickedFile?> pickFileImpl() async {
  final res = await FilePicker.platform.pickFiles(withData: true);
  if (res == null || res.files.isEmpty) return null;
  final f = res.files.first;
  return PickedFile(name: f.name, bytes: f.bytes ?? <int>[]);
}
