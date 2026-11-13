import 'dart:async';

import 'core_bridge.dart';
import 'models.dart';

class PlaybackLoader {
  PlaybackLoader(this.repository);

  final AppRepository repository;
  int _generation = 0;
  bool _closed = false;

  Future<PlaybackPlan?> load(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool localOnly = false,
    bool online = false,
    PlaybackPlan? prepared,
  }) => _load(() async {
    var usedPrepared = false;
    try {
      if (!online) {
        final plan = await repository.localPlayback(drama, episode);
        if (plan != null || localOnly) {
          final uri = plan == null ? null : Uri.tryParse(plan.url);
          final localPath =
              uri != null &&
              plan!.url.trim().isNotEmpty &&
              (uri.scheme.isEmpty ||
                  uri.scheme == 'file' ||
                  RegExp(r'^[a-zA-Z]:[/\\]').hasMatch(plan.url));
          if (plan == null || !plan.local || !localPath) {
            throw AppFailure('本地视频不可用，请重新下载或选择在线播放。', code: 'local_media');
          }
          return plan;
        }
      }
      if (prepared != null && !prepared.local) {
        usedPrepared = true;
        return prepared;
      }
      return online
          ? await repository.resolveOnline(drama, episode, quality: quality)
          : await repository.resolve(drama, episode, quality: quality);
    } finally {
      if (prepared != null && !usedPrepared) {
        unawaited(
          repository.release(prepared.session).catchError((Object _) {}),
        );
      }
    }
  }, waitForCancellation: online);

  Future<PlaybackPlan?> fallback(PlaybackPlan current) =>
      _load(() => repository.fallback(current));

  Future<PlaybackPlan?> selectRoute(PlaybackPlan current, int index) =>
      _load(() => repository.selectPlaybackRoute(current, index));

  Future<PlaybackPlan?> use(PlaybackPlan prepared) =>
      _load(() async => prepared);

  Future<PlaybackPlan?> _load(
    Future<PlaybackPlan> Function() resolve, {
    bool waitForCancellation = true,
  }) async {
    if (_closed) {
      return null;
    }
    final generation = ++_generation;
    try {
      final cancellation = Future<void>.sync(repository.cancelPlayback);
      if (waitForCancellation) {
        await cancellation;
      } else {
        unawaited(cancellation.catchError((Object _) {}));
      }
      if (_closed || generation != _generation) {
        return null;
      }
      final plan = await resolve();
      if (_closed || generation != _generation) {
        await repository.release(plan.session);
        return null;
      }
      return plan;
    } catch (_) {
      if (_closed || generation != _generation) {
        return null;
      }
      rethrow;
    }
  }

  Future<void> close() async {
    _closed = true;
    _generation++;
    await repository.cancelPlayback();
  }
}
