import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';

import 'package:crypto/crypto.dart';
import 'package:ffi/ffi.dart';

class _MediaDigestSink implements Sink<Digest> {
  Digest? result;
  @override
  void add(Digest value) => result = value;
  @override
  void close() {}
}

Future<Digest> mediaFileFingerprint(File file) {
  if (!Platform.isWindows) return sha256.bind(file.openRead()).first;
  final path = file.absolute.path;
  return Isolate.run(() => _windowsMediaFingerprint(path));
}

Digest _windowsMediaFingerprint(String path) {
  if (path.contains('\u0000')) throw FileSystemException('文件路径无效', path);
  final kernel = DynamicLibrary.open('kernel32.dll');
  final create = kernel
      .lookupFunction<
        Pointer<Void> Function(
          Pointer<Utf16>,
          Uint32,
          Uint32,
          Pointer<Void>,
          Uint32,
          Uint32,
          Pointer<Void>,
        ),
        Pointer<Void> Function(
          Pointer<Utf16>,
          int,
          int,
          Pointer<Void>,
          int,
          int,
          Pointer<Void>,
        )
      >('CreateFileW');
  final read = kernel
      .lookupFunction<
        Int32 Function(
          Pointer<Void>,
          Pointer<Uint8>,
          Uint32,
          Pointer<Uint32>,
          Pointer<Void>,
        ),
        int Function(
          Pointer<Void>,
          Pointer<Uint8>,
          int,
          Pointer<Uint32>,
          Pointer<Void>,
        )
      >('ReadFile');
  final close = kernel
      .lookupFunction<
        Int32 Function(Pointer<Void>),
        int Function(Pointer<Void>)
      >('CloseHandle');
  final lastError = kernel.lookupFunction<Uint32 Function(), int Function()>(
    'GetLastError',
  );
  final nativePath = path.replaceAll('/', '\\');
  final extended = nativePath.startsWith('\\\\?\\')
      ? nativePath
      : nativePath.startsWith('\\\\')
      ? '\\\\?\\UNC\\${nativePath.substring(2)}'
      : '\\\\?\\$nativePath';
  final name = extended.toNativeUtf16();
  late final Pointer<Void> handle;
  var openError = 0;
  try {
    handle = create(name, 0x80000000, 7, nullptr, 3, 0x80, nullptr);
    if (handle.address == -1 || handle.address == 0xffffffffffffffff) {
      openError = lastError();
    }
  } finally {
    calloc.free(name);
  }
  if (handle.address == -1 || handle.address == 0xffffffffffffffff) {
    throw FileSystemException(
      '无法读取媒体指纹',
      path,
      OSError('CreateFileW', openError),
    );
  }
  Pointer<Uint8> buffer = nullptr;
  Pointer<Uint32> count = nullptr;
  try {
    buffer = calloc<Uint8>(1024 * 1024);
    count = calloc<Uint32>();
    final result = _MediaDigestSink();
    final digest = sha256.startChunkedConversion(result);
    while (true) {
      if (read(handle, buffer, 1024 * 1024, count, nullptr) == 0) {
        throw FileSystemException(
          '读取媒体指纹失败',
          path,
          OSError('ReadFile', lastError()),
        );
      }
      if (count.value == 0) break;
      digest.add(buffer.asTypedList(count.value));
    }
    digest.close();
    return result.result!;
  } finally {
    close(handle);
    calloc.free(count);
    calloc.free(buffer);
  }
}
