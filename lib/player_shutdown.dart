Future<void> releasePlaybackResources({
  required Future<void> Function() stop,
  required Future<void> Function() dispose,
  required Iterable<Future<void>> pending,
}) async {
  final waiting = Future.wait(
    pending.map((operation) => operation.catchError((Object _) {})),
  );
  try {
    await stop();
  } catch (_) {}
  await waiting;
  await dispose();
}
