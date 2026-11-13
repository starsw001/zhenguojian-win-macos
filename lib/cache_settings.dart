import 'dart:convert';

class CacheSpec {
  const CacheSpec(this.key, this.title, this.minimum, this.unit, this.scope);
  final String key, title, unit, scope;
  final int minimum;
  int get defaultValue => minimum * 2;
  int get maximum => minimum * 8;
  bool get bytes => unit == 'MiB';
}

const cacheSpecs = [
  CacheSpec('catalogMiB', '本地剧库文件', 128, 'MiB', '下次读写生效；不删除旧目录'),
  CacheSpec('sourceRecordsMiB', '站源任务与健康记录', 4, 'MiB', '下次读写生效；不删除历史记录'),
  CacheSpec('coverMiB', '海报磁盘缓存容量', 256, 'MiB', '保存后生效；超过上限淘汰旧海报缓存'),
  CacheSpec('coverEntries', '海报缓存数量', 2000, '张', '保存后生效；与海报容量同时约束'),
  CacheSpec('rankingMiB', '榜单磁盘缓存容量', 8, 'MiB', '保存后生效；只淘汰可重新获取的榜单缓存'),
  CacheSpec('rankingPages', '榜单内存缓存页数', 128, '页', '保存后生效；按最旧页面淘汰'),
  CacheSpec(
    'suggestionEntries',
    '红果搜索联想缓存',
    128,
    '个关键词',
    '用于后续联想请求；超过上限淘汰旧关键词',
  ),
  CacheSpec('inputEntries', '搜索输入联想缓存', 32, '个关键词', '每个搜索框；保存后生效'),
  CacheSpec('preloadMiB', '单次播放预加载', 4, 'MiB', '下次预加载生效；占用显示所有会话合计'),
  CacheSpec('playerMiB', '桌面播放器缓冲', 32, 'MiB', '每个播放器；下次新建播放页生效，不中断当前播放'),
];

class CacheLimits {
  const CacheLimits([this.values = const {}]);
  final Map<String, int> values;
  int value(String key) =>
      values[key] ?? cacheSpecs.firstWhere((s) => s.key == key).defaultValue;
  factory CacheLimits.fromJson(dynamic json) => CacheLimits({
    for (final spec in cacheSpecs)
      spec.key: json is Map && json[spec.key] is num
          ? (json[spec.key] as num).toInt().clamp(spec.minimum, spec.maximum)
          : spec.defaultValue,
  });
  Map<String, int> toJson() => {
    for (final spec in cacheSpecs) spec.key: value(spec.key),
  };
}

class CacheUsage {
  const CacheUsage({this.value = 0, this.available = false, this.detail = ''});
  final int value;
  final bool available;
  final String detail;
  factory CacheUsage.fromJson(Map json) => CacheUsage(
    value: (json['value'] as num?)?.toInt() ?? 0,
    available: json['available'] == true,
    detail: json['detail'] as String? ?? '',
  );
  String label(CacheSpec spec) {
    if (!available) return '暂无法读取';
    if (!spec.bytes) return '$value ${spec.unit}';
    return '${(value / (1 << 20)).toStringAsFixed(2)} MiB';
  }
}

class _PlayerCacheProbe {
  _PlayerCacheProbe(this.limitMiB, this.read);
  final int limitMiB;
  final Future<int?> Function() read;
}

class CacheRuntime {
  static CacheLimits limits = const CacheLimits();
  static final _inputs = <Object, Map<String, List<String>>>{};
  static final _players = <Object, _PlayerCacheProbe>{};

  static void apply(CacheLimits value) {
    limits = CacheLimits(
      Map.unmodifiable(CacheLimits.fromJson(value.toJson()).toJson()),
    );
    for (final cache in _inputs.values) {
      while (cache.length > limits.value('inputEntries')) {
        cache.remove(cache.keys.first);
      }
    }
  }

  static void registerInput(Object owner, Map<String, List<String>> cache) =>
      _inputs[owner] = cache;
  static void unregisterInput(Object owner) => _inputs.remove(owner);
  static void registerPlayer(
    Object owner,
    int limitMiB,
    Future<int?> Function() read,
  ) => _players[owner] = _PlayerCacheProbe(limitMiB, read);
  static void unregisterPlayer(Object owner) => _players.remove(owner);

  static int? playerBytes(String raw) {
    try {
      final state = jsonDecode(raw);
      if (state is! Map) return null;
      if (state['total-bytes'] case final num bytes) {
        return bytes >= 0 ? bytes.toInt() : null;
      }
      final forward = state['fw-bytes'], backward = state['bw-bytes'];
      if (forward is num && backward is num && forward >= 0 && backward >= 0) {
        return (forward + backward).toInt();
      }
    } catch (_) {}
    return null;
  }

  static Future<int?> readPlayerBytes(
    Future<String> Function(String) get,
  ) async {
    final direct = int.tryParse(await get('demuxer-cache-state/total-bytes'));
    if (direct != null && direct >= 0) return direct;
    return playerBytes(await get('demuxer-cache-state'));
  }

  static Future<Map<String, CacheUsage>> sampleUsage() async {
    final total = _inputs.values.fold(0, (sum, cache) => sum + cache.length);
    var playerTotal = 0;
    var readable = true;
    final players = Map<Object, _PlayerCacheProbe>.of(_players);
    for (final entry in players.entries) {
      try {
        final bytes = await entry.value.read().timeout(
          const Duration(seconds: 2),
        );
        if (!identical(_players[entry.key], entry.value)) continue;
        if (bytes == null) {
          readable = false;
        } else {
          playerTotal += bytes;
        }
      } catch (_) {
        readable = false;
      }
    }
    return {
      'inputEntries': CacheUsage(
        value: total,
        available: true,
        detail: '${_inputs.length} 个搜索框合计；上限按每个搜索框计算',
      ),
      'playerMiB': CacheUsage(
        value: playerTotal,
        available: readable,
        detail: players.isEmpty
            ? '无活动桌面播放器；仅统计解复用缓存，不包括解码画面内存'
            : '活动播放器的缓存合计；各自上限 ${players.values.map((p) => '${p.limitMiB} MiB').join('、')}。新设置用于下次新建播放页。',
      ),
    };
  }
}
