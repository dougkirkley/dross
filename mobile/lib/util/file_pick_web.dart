// dart:html is the pragmatic way to open a native file dialog on Flutter web;
// this file is only ever imported on web (conditional import in file_pick.dart).
// ignore_for_file: deprecated_member_use, avoid_web_libraries_in_flutter
import 'dart:async';
import 'dart:html' as html;
import 'dart:typed_data';

import 'file_pick.dart';

/// Web file pick via a hidden <input type=file>. click() is called
/// synchronously so it counts as a user gesture (required by mobile Safari).
Future<PickedFile?> pickFileImpl() async {
  final input = html.FileUploadInputElement();
  input.click(); // must run synchronously in the tap gesture
  await input.onChange.first;
  final files = input.files;
  if (files == null || files.isEmpty) return null;
  final f = files.first;

  final reader = html.FileReader();
  reader.readAsArrayBuffer(f);
  await reader.onLoadEnd.first;
  final result = reader.result;
  final bytes =
      result is ByteBuffer ? result.asUint8List() : (result as Uint8List);
  return PickedFile(name: f.name, bytes: bytes);
}
