import 'dart:async';
import 'dart:ffi' as ffi;
import 'dart:io';
import 'dart:isolate';

import 'package:path/path.dart' as path;
import 'package:ffi/ffi.dart';

import 'core_bridge.dart';
import 'media_pipeline.dart';

class CoverDecoder {
  CoverDecoder({
    MediaExecutor Function()? createExecutor,
    Future<void> Function(String, File)? convertAvif,
  }) : _createExecutor = createExecutor ?? FFmpegExecutor.new,
       _convertAvif = convertAvif ?? _writeAvifAsJPEG;
  final MediaExecutor Function() _createExecutor;
  final Future<void> Function(String, File) _convertAvif;
  final _pending = <String, Future<String>>{};
  Future<void> _tail = Future.value();

  Future<String> convert(
    String original,
    Future<Map<String, dynamic>> Function() prepare, {
    bool force = false,
  }) async {
    final stat = await File(original).stat();
    if (stat.type != FileSystemEntityType.file || stat.size <= 0) {
      throw AppFailure('海报缓存已失效，请重试');
    }
    final directory = Directory(
      path.join(path.dirname(original), 'compatible-v1'),
    );
    final key =
        '${path.basenameWithoutExtension(original)}-${stat.modified.microsecondsSinceEpoch}-${stat.size}';
    final target = File(path.join(directory.path, '$key.jpg'));
    if (!force && await _isJPEG(target)) {
      await target.setLastModified(DateTime.now());
      return target.path;
    }
    if (_pending.containsKey(key)) return _pending[key]!;
    if (_pending.length >= 96) throw AppFailure('海报正在处理，请稍后重试');
    final result = _tail.then((_) => _convert(original, target, prepare));
    _tail = result.then<void>(
      (_) {},
      onError: (Object error, StackTrace stack) {},
    );
    _pending[key] = result;
    try {
      return await result;
    } finally {
      if (identical(_pending[key], result)) _pending.remove(key);
    }
  }

  Future<bool> _isJPEG(File file) async {
    try {
      final length = await file.length();
      if (length < 4 || length > 4 * 1024 * 1024) return false;
      final handle = await file.open();
      try {
        final header = await handle.read(3);
        return header.length == 3 &&
            header[0] == 255 &&
            header[1] == 216 &&
            header[2] == 255;
      } finally {
        await handle.close();
      }
    } on FileSystemException {
      return false;
    }
  }

  Future<String> _convert(
    String original,
    File target,
    Future<Map<String, dynamic>> Function() prepare,
  ) async {
    final prepared = await prepare();
    final heic = prepared['heic'] == true;
    if (!heic && prepared['avif'] != true) {
      return prepared['path'] as String? ?? original;
    }
    final input = prepared['input'] as String? ?? '';
    if (input.isEmpty) throw AppFailure('海报格式转换失败，请重试');
    final intermediate = File('${target.path}.part');
    final executor = heic ? _createExecutor() : null;
    Future<void>? work;
    try {
      await target.parent.create(recursive: true);
      final orientation = prepared['filters'] as String? ?? '';
      final filters = [
        if (orientation.isNotEmpty) orientation,
        "scale=w='min(800,iw)':h='min(800,ih)':force_original_aspect_ratio=decrease",
      ].join(',');
      work = !heic
          ? _convertAvif(input, intermediate)
          : executor!.run([
              '-max_alloc',
              '67108864',
              '-threads',
              '1',
              if (heic) ...['-f', 'hevc'],
              '-i',
              input,
              '-frames:v',
              '1',
              '-an',
              '-sn',
              '-vf',
              filters,
              '-filter_threads',
              '1',
              '-threads',
              '1',
              '-c:v',
              'mjpeg',
              '-pix_fmt',
              'yuvj420p',
              '-q:v',
              '3',
              '-f',
              'image2',
              intermediate.path,
            ]);
      await work.timeout(const Duration(seconds: 12));
      if (!await _isJPEG(intermediate)) throw AppFailure('海报转换未生成有效图片');
      if (await target.exists()) await target.delete();
      await intermediate.rename(target.path);
      await _prune(target.parent, target.path);
      return target.path;
    } on TimeoutException {
      await executor?.cancel();
      try {
        await work?.timeout(const Duration(seconds: 5));
      } catch (_) {}
      throw AppFailure('海报转换超时，请重试');
    } finally {
      for (final file in [if (input != original) File(input), intermediate]) {
        try {
          if (await file.exists()) await file.delete();
        } on FileSystemException {
          continue;
        }
      }
    }
  }

  static Future<void> _writeAvifAsJPEG(String input, File output) async {
    final temporary = await output.parent.createTemp('.avif-');
    if (!path.isWithin(output.parent.absolute.path, temporary.absolute.path)) {
      throw AppFailure('海报转换缓存目录无效');
    }
    try {
      final library = path.join(
        File(Platform.resolvedExecutable).parent.path,
        'libmpv-2.dll',
      );
      final directory = temporary.path;
      final bytes = await Isolate.run(
        () => _decodeAvifNative(library, input, directory),
      );
      await output.writeAsBytes(bytes, flush: true);
    } finally {
      await temporary.delete(recursive: true);
    }
  }

  static Future<List<int>> _decodeAvifNative(
    String library,
    String input,
    String directory,
  ) async {
    final mpv = ffi.DynamicLibrary.open(library);
    final create = mpv
        .lookupFunction<
          ffi.Pointer<ffi.Void> Function(),
          ffi.Pointer<ffi.Void> Function()
        >('mpv_create');
    final option = mpv
        .lookupFunction<
          ffi.Int32 Function(
            ffi.Pointer<ffi.Void>,
            ffi.Pointer<Utf8>,
            ffi.Pointer<Utf8>,
          ),
          int Function(
            ffi.Pointer<ffi.Void>,
            ffi.Pointer<Utf8>,
            ffi.Pointer<Utf8>,
          )
        >('mpv_set_option_string');
    final initialize = mpv
        .lookupFunction<
          ffi.Int32 Function(ffi.Pointer<ffi.Void>),
          int Function(ffi.Pointer<ffi.Void>)
        >('mpv_initialize');
    final command = mpv
        .lookupFunction<
          ffi.Int32 Function(
            ffi.Pointer<ffi.Void>,
            ffi.Pointer<ffi.Pointer<Utf8>>,
          ),
          int Function(ffi.Pointer<ffi.Void>, ffi.Pointer<ffi.Pointer<Utf8>>)
        >('mpv_command');
    final dispose = mpv
        .lookupFunction<
          ffi.Void Function(ffi.Pointer<ffi.Void>),
          void Function(ffi.Pointer<ffi.Void>)
        >('mpv_terminate_destroy');
    final handle = create();
    if (handle == ffi.nullptr) throw AppFailure('本机海报解码器不可用');
    try {
      final options = {
        'config': 'no',
        'load-scripts': 'no',
        'audio': 'no',
        'vo': 'image',
        'vo-image-format': 'jpg',
        'vo-image-outdir': directory,
        'frames': '1',
        'image-display-duration': '0.1',
        'idle': 'yes',
        'hwdec': 'no',
      };
      for (final entry in options.entries) {
        final name = entry.key.toNativeUtf8(),
            value = entry.value.toNativeUtf8();
        try {
          if (option(handle, name, value) < 0) {
            throw AppFailure('AVIF 海报解码设置失败：${entry.key}');
          }
        } finally {
          calloc.free(name);
          calloc.free(value);
        }
      }
      if (initialize(handle) < 0) throw AppFailure('AVIF 海报解码器初始化失败');
      final args = calloc<ffi.Pointer<Utf8>>(3);
      args[0] = 'loadfile'.toNativeUtf8();
      args[1] = input.toNativeUtf8();
      args[2] = ffi.nullptr;
      try {
        if (command(handle, args) < 0) throw AppFailure('AVIF 海报读取失败');
      } finally {
        calloc.free(args[0]);
        calloc.free(args[1]);
        calloc.free(args);
      }
      final watch = Stopwatch()..start();
      while (watch.elapsed < const Duration(seconds: 8)) {
        await for (final file in Directory(
          directory,
        ).list(followLinks: false)) {
          if (file is! File || !file.path.endsWith('.jpg')) continue;
          try {
            final length = await file.length();
            if (length < 4 || length > 4 * 1024 * 1024) continue;
            final bytes = await file.readAsBytes();
            if (bytes.length >= 4 &&
                bytes[0] == 255 &&
                bytes[1] == 216 &&
                bytes[bytes.length - 2] == 255 &&
                bytes.last == 217) {
              return bytes;
            }
          } on FileSystemException {
            continue;
          }
        }
        await Future<void>.delayed(const Duration(milliseconds: 25));
      }
      throw AppFailure('AVIF 海报解码超时');
    } finally {
      dispose(handle);
    }
  }

  Future<void> _prune(Directory directory, String keep) async {
    final entries = <(File, FileStat)>[];
    var size = 0;
    await for (final entry in directory.list(followLinks: false)) {
      if (entry is! File) continue;
      final stat = await entry.stat();
      if (entry.path.endsWith('.part')) {
        if (DateTime.now().difference(stat.modified) >
            const Duration(minutes: 1)) {
          await entry.delete();
        }
      } else if (entry.path.endsWith('.jpg')) {
        entries.add((entry, stat));
        size += stat.size;
      }
    }
    entries.sort((a, b) => a.$2.modified.compareTo(b.$2.modified));
    var count = entries.length;
    for (final entry in entries) {
      if (count <= 128 && size <= 64 * 1024 * 1024) break;
      if (entry.$1.path == keep) continue;
      await entry.$1.delete();
      count--;
      size -= entry.$2.size;
    }
  }
}
