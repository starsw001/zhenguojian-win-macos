import 'update_activity.dart';
import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import 'core_bridge.dart';
import 'models.dart';
import 'player_runtime_options.dart';

class SourcePlaybackCheck {
  SourcePlaybackCheck(
    this.repository, {
    this.onVideo,
    this.onStage,
    this.renderVideo = true,
  });
  final AppRepository repository;
  final bool renderVideo;
  final ValueChanged<VideoController?>? onVideo;
  final ValueChanged<String>? onStage;
  bool cancelled = false;

  Future<void> cancel() async {
    cancelled = true;
    await repository.cancelCatalog();
    await repository.cancelPlayback();
  }

  String _safe(Object value) {
    final text = '$value'.replaceAllMapped(
      RegExp(r'https?://[^\s<>]+'),
      (match) => Uri.tryParse(match[0]!)?.host ?? 'HOST',
    );
    return String.fromCharCodes(text.runes.take(1800));
  }

  Future<Map<String, dynamic>> run(SourceSite source) async {
    final mode = (await repository.resourceSettings()).proxyMode;
    final steps = <Map<String, dynamic>>[];
    final samples = <Map<String, dynamic>>[];
    String sample = '';
    Drama? sampleDrama;
    Future<T?> stage<T>(
      String name,
      Future<(T, String)> Function() action,
    ) async {
      onStage?.call(name);
      final timer = Stopwatch()..start();
      final step = <String, dynamic>{'name': name};
      steps.add(step);
      try {
        if (cancelled) throw AppFailure('检测已停止');
        final (result, message) = await action();
        step.addAll({'state': 'ok', 'message': _safe(message)});
        return result;
      } catch (error) {
        step.addAll({'state': 'failed', 'message': _safe(error)});
        return null;
      } finally {
        step['elapsedMs'] = timer.elapsedMilliseconds;
      }
    }

    void skip(String name, String reason) {
      steps.add({
        'name': name,
        'state': 'skipped',
        'message': reason,
        'elapsedMs': 0,
      });
    }

    final catalog = await stage('浏览', () async {
      final page = await repository.catalog(source.id, force: true);
      if (page.items.isEmpty) throw AppFailure('站源未返回可浏览剧集');
      return (
        page,
        '${page.items.length} 部 · ${page.warning.isEmpty ? '已读取真实目录' : page.warning}',
      );
    });
    DramaDetail? detail;
    if (catalog == null) {
      skip('搜索', '目录不可用，尚无真实剧名可检索');
      skip('选集', '目录不可用，未取得剧集');
      skip('实际播放', '未取得分集，不声称播放通过');
    } else {
      final drama = catalog.items.firstWhere(
        (item) => item.vipStatus != true,
        orElse: () => catalog.items.first,
      );
      sample = drama.title;
      sampleDrama = drama;
      final query = String.fromCharCodes(drama.title.runes.take(4));
      await stage('搜索', () async {
        var usedQuery = query;
        var page = await repository.catalog(source.id, query: query);
        if (page.items.isEmpty &&
            !page.localSearch &&
            page.warning.isEmpty &&
            query != drama.title) {
          usedQuery = drama.title;
          page = await repository.catalog(source.id, query: usedQuery);
        }
        if (page.items.isEmpty) {
          throw AppFailure('真实剧名“$usedQuery”未返回匹配结果');
        }
        if (source.onlineSearch && page.localSearch) {
          throw AppFailure(
            '${page.warning.isEmpty ? '在线检索未找到结果，仅本机匹配' : page.warning} · ${page.items.length} 条',
          );
        }
        return (
          page,
          '${usedQuery == query ? '' : '短关键词无结果，使用完整剧名复核；'}'
              '“$usedQuery” ${page.items.length} 条 · ${source.onlineSearch ? '在线检索' : '本机目录筛选（站源无在线搜索）'}',
        );
      });
      detail = await stage('选集', () async {
        final value = await repository.detail(drama);
        if (value.episodes.where((episode) => !episode.vip).isEmpty) {
          throw AppFailure('未取得免费可播放分集，不绕过付费授权');
        }
        return (
          value,
          '${value.episodes.length} 集 · ${value.rawEpisodes.length} 条原始线路记录',
        );
      });
      if (detail == null) {
        skip('实际播放', '分集不可用，不声称播放通过');
      } else {
        final selected = detail;
        await stage('实际播放', () async {
          final failures = <String>[];
          final distinct = <int, Episode>{};
          for (final episode in selected.episodes.where((item) => !item.vip)) {
            distinct.putIfAbsent(episode.number, () => episode);
          }
          for (final episode in distinct.values.take(2)) {
            if (cancelled) throw AppFailure('检测已停止');
            PlaybackPlan? plan;
            try {
              plan = await repository.resolve(
                selected.drama,
                episode,
                quality: 480,
              );
              samples.add(await _decode(plan, episode.number));
            } catch (error) {
              failures.add('第${episode.number}集：${_safe(error)}');
            } finally {
              if (plan != null && plan.session.isNotEmpty) {
                await repository.release(plan.session);
              }
            }
          }
          if (failures.isNotEmpty) {
            throw AppFailure('${samples.length} 集实播成功；${failures.join('；')}');
          }
          return (
            samples,
            samples
                .map(
                  (item) =>
                      '第${item['episode']}集 ${item['width']}×${item['height']}，进度 ${item['startMs']}→${item['endMs']}ms',
                )
                .join('；'),
          );
        });
      }
    }
    final allPassed = steps.every((step) => step['state'] == 'ok');
    final state = allPassed
        ? 'decoded'
        : samples.isNotEmpty
        ? 'partial'
        : 'failed';
    final health = <String, dynamic>{
      'checkedAt': DateTime.now().toUtc().toIso8601String(),
      'state': state,
      'networkMode': mode,
      'sample': sample,
      'steps': steps,
    };
    final status = await repository.saveSourceHealth(source.id, health);
    return {
      'source': source.id,
      'name': source.name,
      'health': health,
      'decodedSamples': samples,
      if (sampleDrama != null) 'sampleDrama': sampleDrama.toJson(),
      'saved': status.storageError.isEmpty,
      'remoteMedia': true,
      'remoteImagesRequested': false,
    };
  }

  Future<Map<String, dynamic>> verifyPlayback(PlaybackPlan plan, int episode) =>
      _decode(plan, episode);

  Future<Map<String, dynamic>> _decode(PlaybackPlan plan, int episode) async {
    final player = Player(
      configuration: const PlayerConfiguration(title: '站源真实播放检查'),
    );
    final controller = renderVideo ? VideoController(player) : null;
    onVideo?.call(controller);
    final errors = <String>[];
    final decoderLogs = <String>[];
    final subscription = player.stream.error.listen(errors.add);
    final logSubscription = player.stream.log.listen((log) {
      if (log.level == 'error' || log.level == 'fatal') {
        decoderLogs.add('${log.prefix}: ${log.text.trim()}');
        if (decoderLogs.length > 8) decoderLogs.removeAt(0);
      }
    });
    UpdateActivity.enterPlayer();
    try {
      await player.setVolume(0);
      final platform = player.platform;
      if (platform is NativePlayer) {
        await configurePortablePlaybackCache(platform.setProperty);
        if (!renderVideo) await platform.setProperty('vid', 'auto');
        await platform.setProperty('network-timeout', '15');
        await platform.setProperty(
          'demuxer-lavf-o',
          [
            'seg_max_retry=3',
            'strict=experimental',
            'allowed_extensions=ALL',
            'protocol_whitelist=[http,https,tcp,tls,crypto,data,file]',
            if (plan.decryptionKey.isNotEmpty)
              'decryption_key=${plan.decryptionKey}',
          ].join(','),
        );
      }
      await player
          .open(Media(plan.url, httpHeaders: plan.headers))
          .timeout(const Duration(seconds: 30));
      final timer = Stopwatch()..start();
      int? start;
      while (timer.elapsed < const Duration(seconds: 35)) {
        if (cancelled) throw AppFailure('检测已停止');
        final state = player.state;
        if ((state.width ?? 0) > 0 &&
            (state.height ?? 0) > 0 &&
            state.position.inMilliseconds >= 500) {
          start ??= state.position.inMilliseconds;
          if (state.position.inMilliseconds - start >= 1500) {
            return {
              'episode': episode,
              'width': state.width,
              'height': state.height,
              'startMs': start,
              'endMs': state.position.inMilliseconds,
            };
          }
        }
        if (errors.isNotEmpty && !state.buffering && !state.playing) {
          throw AppFailure(_safe([...decoderLogs, errors.last].join('；')));
        }
        await Future<void>.delayed(const Duration(milliseconds: 100));
      }
      final state = player.state;
      throw AppFailure(
        _safe(
          [
            '真实媒体解码超时：${state.width ?? 0}×${state.height ?? 0}，'
                '进度 ${state.position.inMilliseconds}ms，'
                '播放 ${state.playing}，缓冲 ${state.buffering}',
            ...decoderLogs,
            ...errors,
          ].join('；'),
        ),
      );
    } finally {
      UpdateActivity.leavePlayer();
      await subscription.cancel();
      await logSubscription.cancel();
      await player.dispose();
      onVideo?.call(null);
    }
  }
}
