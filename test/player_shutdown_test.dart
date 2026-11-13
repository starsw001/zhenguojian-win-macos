import 'dart:async';

import 'package:duanju_app/player_shutdown.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('stop releases media before pending playback work completes', () async {
    final pending = Completer<void>();
    final events = <String>[];
    final closing = releasePlaybackResources(
      stop: () async => events.add('stop'),
      dispose: () async => events.add('dispose'),
      pending: [pending.future],
    );
    await Future<void>.delayed(Duration.zero);
    expect(events, ['stop']);
    pending.complete();
    await closing;
    expect(events, ['stop', 'dispose']);
  });

  test('stop and queued-operation errors do not skip final disposal', () async {
    final events = <String>[];
    final failed = Completer<void>();
    final closing = releasePlaybackResources(
      stop: () async {
        events.add('stop');
        throw StateError('synthetic stop failure');
      },
      dispose: () async => events.add('dispose'),
      pending: [failed.future],
    );
    await Future<void>.delayed(Duration.zero);
    failed.completeError(StateError('synthetic queued failure'));
    await closing;
    expect(events, ['stop', 'dispose']);
  });

  test('final disposal waits for stopping and each pending resource', () async {
    final stop = Completer<void>();
    final work = Completer<void>();
    final rate = Completer<void>();
    var disposed = false;
    final closing = releasePlaybackResources(
      stop: () => stop.future,
      dispose: () async => disposed = true,
      pending: [work.future, rate.future],
    );
    work.complete();
    rate.complete();
    await Future<void>.delayed(Duration.zero);
    expect(disposed, isFalse);
    stop.complete();
    await closing;
    expect(disposed, isTrue);
  });

  test(
    'pending failures are observed while stop is still completing',
    () async {
      final stopping = Completer<void>();
      final pending = Completer<void>();
      var disposed = false;
      final closing = releasePlaybackResources(
        stop: () => stopping.future,
        dispose: () async => disposed = true,
        pending: [pending.future],
      );
      pending.completeError(StateError('synthetic pending failure'));
      await Future<void>.delayed(Duration.zero);
      stopping.complete();
      await closing;
      expect(disposed, isTrue);
    },
  );
}
