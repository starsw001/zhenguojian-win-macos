import 'models.dart';

final _sourceProgressAddress = RegExp(
  r'''https?://[^\s<>"'；，。·]+''',
  caseSensitive: false,
);

final _sourceProgressHost = RegExp(
  r'''\b(?:(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}|(?:\d{1,3}\.){3}\d{1,3})(?::\d{1,5})?(?:/[^\s<>"'；，。·]*)?''',
  caseSensitive: false,
);

String sourceProgressText(String value) => value
    .replaceAll(_sourceProgressAddress, '')
    .replaceAllMapped(_sourceProgressHost, (match) {
      final address = match.group(0)!;
      if (RegExp(
        r'\.(?:log|json|txt|exe|dll|mp4|m3u8|dart|go)(?:\.\d+)?$',
        caseSensitive: false,
      ).hasMatch(address))
        return address;
      return '';
    })
    .replaceAll(RegExp(r'\s*·\s*(?=[；;]|$)'), '')
    .trim();

DateTime? sourceTime(Object? value) {
  final time = DateTime.tryParse('$value');
  return time != null && time.year >= 2000 ? time.toLocal() : null;
}

class SourceHealthStep {
  SourceHealthStep.fromJson(Map<String, dynamic> json)
    : name = json['name'] as String? ?? '',
      state = json['state'] as String? ?? '',
      message = json['message'] as String? ?? '',
      host = json['host'] as String? ?? '',
      httpStatus = intValue(json['httpStatus']),
      elapsedMs = intValue(json['elapsedMs']),
      cfRay = json['cfRay'] as String? ?? '';

  final String name, state, message, host, cfRay;
  final int httpStatus, elapsedMs;
}

class SourceHealth {
  SourceHealth.fromJson(Map<String, dynamic> json)
    : checkedAt = sourceTime(json['checkedAt']),
      networkMode = json['networkMode'] as String? ?? '',
      downloadCheck = json['downloadCheck'] is Map
          ? SourceHealthStep.fromJson(
              Map<String, dynamic>.from(json['downloadCheck'] as Map),
            )
          : null,
      state = json['state'] as String? ?? '',
      sample = json['sample'] as String? ?? '',
      steps = [
        for (final step in json['steps'] as List? ?? [])
          SourceHealthStep.fromJson(Map<String, dynamic>.from(step as Map)),
      ];

  final DateTime? checkedAt;
  final String state, sample, networkMode;
  final SourceHealthStep? downloadCheck;
  final List<SourceHealthStep> steps;
  String get label => switch (state) {
    'ok' => '连接检测通过 · 未做播放器实播',
    'catalogOnly' => '目录正常 · 播放未检测',
    'checking' => '检测中',
    'decoded' => '真实浏览、搜索、选集与实播通过',
    'partial' => '部分通过 · 查看各步骤',
    'failed' => '检测未通过',
    _ => '尚未检测',
  };
}

class SourceStatus {
  final String catalogRule;
  final int catalogPages, catalogFetched;
  SourceStatus.fromJson(Map<String, dynamic> json)
    : catalogRule = json['catalogRule'] as String? ?? '',
      catalogPages = intValue(json['catalogPages']),
      catalogFetched = intValue(json['catalogFetched']),
      catalogOrigin = json['catalogOrigin'] as String? ?? '',
      catalogFullAt = sourceTime(json['catalogFullAt']),
      source = json['source'] as String? ?? '',
      count = intValue(json['count']),
      unknownVip = intValue(json['unknownVip']),
      page = intValue(json['page']),
      hasMore = json['hasMore'] == true,
      updatedAt = sourceTime(json['updatedAt']),
      running = json['running'] == true,
      operation = json['operation'] as String? ?? '',
      stage = json['stage'] as String? ?? '',
      completed = intValue(json['completed']),
      total = intValue(json['total']),
      added = intValue(json['added']),
      error = json['error'] as String? ?? '',
      storageError = json['storageError'] as String? ?? '',
      startedAt = sourceTime(json['startedAt']),
      finishedAt = sourceTime(json['finishedAt']),
      retryAt = sourceTime(json['retryAt']),
      mirrors = [
        for (final row in json['mirrors'] as List? ?? [])
          SourceMirror.fromJson(Map<String, dynamic>.from(row as Map)),
      ],
      mirrorPublications = [
        for (final row in json['mirrorPublications'] as List? ?? [])
          SourceMirrorPublication.fromJson(
            Map<String, dynamic>.from(row as Map),
          ),
      ],
      healthHistory = [
        for (final row in json['healthHistory'] as List? ?? [])
          SourceHealth.fromJson(Map<String, dynamic>.from(row as Map)),
      ],
      health = json['health'] is Map
          ? SourceHealth.fromJson(
              Map<String, dynamic>.from(json['health'] as Map),
            )
          : null;

  final String source, operation, stage, error, storageError;
  final String catalogOrigin;
  final DateTime? catalogFullAt;
  final int count, page, completed, total, added, unknownVip;
  final bool hasMore, running;
  final DateTime? updatedAt, startedAt, finishedAt, retryAt;
  final SourceHealth? health;
  final List<SourceHealth> healthHistory;
  final List<SourceMirror> mirrors;
  final List<SourceMirrorPublication> mirrorPublications;

  String get displayStage => sourceProgressText(stage);
  String get displayError => sourceProgressText(error);

  int get retrySeconds {
    if (retryAt == null) return 0;
    return ((retryAt!.difference(DateTime.now()).inMilliseconds / 1000).ceil())
        .clamp(0, 3600);
  }
}

class SourceMirror {
  SourceMirror.fromJson(Map<String, dynamic> json)
    : mediaReachable = json['mediaReachable'] == true,
      origin = '${json['origin'] ?? ''}',
      kind = '${json['kind'] ?? 'unknown'}',
      state = '${json['state'] ?? 'pending'}',
      message = '${json['message'] ?? ''}',
      catalogMessage = '${json['catalogMessage'] ?? ''}',
      addedOn = '${json['addedOn'] ?? ''}',
      addedFrom = '${json['addedFrom'] ?? ''}',
      lastError = '${json['lastError'] ?? ''}',
      effectiveOrigin = '${json['effectiveOrigin'] ?? ''}',
      duplicateOf = '${json['duplicateOf'] ?? ''}',
      poster = json['poster'] == true,
      posterWidth = intValue(json['posterWidth']),
      posterHeight = intValue(json['posterHeight']),
      posterBytes = intValue(json['posterBytes']),
      posterMessage = '${json['posterMessage'] ?? ''}',
      browse = json['browse'] == true,
      selection = json['selection'] == true,
      playback = json['playback'] == true,
      width = intValue(json['width']),
      height = intValue(json['height']),
      positionMs = intValue(json['positionMs']),
      checkedAt = sourceTime(json['checkedAt']);

  final String origin, kind, state, message, addedOn, addedFrom, lastError;
  final String catalogMessage;
  final String effectiveOrigin;
  final String duplicateOf, posterMessage;
  final bool poster;
  final int posterWidth, posterHeight, posterBytes;
  final bool mediaReachable;
  final bool browse, selection, playback;
  final int width, height, positionMs;
  final DateTime? checkedAt;
  String get kindLabel => switch (kind) {
    'huangguoai' => '黄果AI',
    'huangguo-video' => '黄果视频',
    'publication' => '地址发布页 · 不是视频站',
    _ => '类型待确认',
  };
  bool get connectionOnly =>
      kind == 'huangguo-video' &&
      state == 'connected' &&
      duplicateOf.isEmpty &&
      poster &&
      posterWidth > 0 &&
      posterHeight > 0 &&
      posterBytes > 0 &&
      browse &&
      selection &&
      mediaReachable;
  bool get usable =>
      connectionOnly ||
      state == 'ready' &&
          duplicateOf.isEmpty &&
          poster &&
          posterWidth > 0 &&
          posterHeight > 0 &&
          posterBytes > 0 &&
          browse &&
          selection &&
          playback &&
          width > 0 &&
          height > 0 &&
          positionMs >= 400;
  String get label => duplicateOf.isNotEmpty
      ? (state == 'failed' ? '重复地址 · 失败原因保留' : '重复地址 · 已合并')
      : connectionOnly
      ? '线路连通 · 未实播 · 可自动切换'
      : usable
      ? '四项通过 · 可自动切换'
      : switch (state) {
          'failed' => '失败 · 地址保留',
          'checking' => '检测中',
          _ => '待核验 · 尚未启用',
        };
}

class SourceMirrorPublication {
  SourceMirrorPublication.fromJson(Map<String, dynamic> json)
    : url = '${json['url'] ?? ''}',
      state = '${json['state'] ?? ''}',
      message = '${json['message'] ?? ''}',
      added = intValue(json['added']),
      checkedAt = sourceTime(json['checkedAt']);
  final String url, state, message;
  final int added;
  final DateTime? checkedAt;
  String get kindLabel =>
      const {
        'huangguoai.com',
        'huangguoai.ai',
      }.contains(Uri.tryParse(url)?.host.toLowerCase())
      ? '永久入口'
      : '地址发布页';
}
