import 'package:path/path.dart' as p;

Map<String, String> programStorageDefaults({
  required bool windows,
  required String executable,
  bool isolated = false,
}) {
  if (!windows || isolated) return const {};
  final paths = p.Context(style: p.Style.windows);
  if (!paths.isAbsolute(executable)) {
    throw ArgumentError.value(executable, 'executable', '软件路径必须是绝对路径');
  }
  final directory = paths.dirname(executable);
  return {
    'downloadDirectory': paths.join(directory, 'download'),
    'defaultLogDirectory': paths.join(directory, 'logs'),
  };
}
