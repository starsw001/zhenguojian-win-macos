import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import 'core_bridge.dart';
import 'local_store.dart';
import 'models.dart';
import 'piguo_access_screen.dart';
import 'source_status.dart';
import 'source_intervention.dart';
import 'source_playback_check.dart';
import 'sorani_login_screen.dart';

String sourceTimestamp(DateTime? value) {
  if (value == null) return '尚无记录';
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.month}/${local.day} ${two(local.hour)}:${two(local.minute)}';
}

class HuangguoMirrorsPanel extends StatelessWidget {
  const HuangguoMirrorsPanel({super.key, required this.status});

  final SourceStatus status;

  @override
  Widget build(BuildContext context) {
    final ready = status.mirrors.where((item) => item.usable).length;
    final unique = status.mirrors
        .where((item) => item.duplicateOf.isEmpty)
        .toList();
    final aliases = <String, List<SourceMirror>>{};
    for (final item in status.mirrors.where(
      (item) => item.duplicateOf.isNotEmpty,
    )) {
      aliases.putIfAbsent(item.duplicateOf, () => []).add(item);
    }
    return ExpansionTile(
      key: PageStorageKey('huangguo-mirrors-${status.source}'),
      tilePadding: EdgeInsets.zero,
      title: Text('备用线路（${unique.length}）'),
      subtitle: Text(
        '$ready 条四项通过；已合并 ${status.mirrors.length - unique.length} 个重复地址，失败原因保留',
      ),
      children: [
        for (final item in unique)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _HuangguoMirrorEntry(
                    item: item,
                    source: status.source,
                    label: '线路 ${unique.indexOf(item) + 1}',
                  ),
                  if (aliases[item.origin]?.isNotEmpty == true)
                    ExpansionTile(
                      key: PageStorageKey(
                        'mirror-aliases-${status.source}-${item.origin}',
                      ),
                      title: Text('已合并的重复地址（${aliases[item.origin]!.length}）'),
                      children: [
                        for (final alias in aliases[item.origin]!)
                          Padding(
                            padding: const EdgeInsets.symmetric(vertical: 8),
                            child: _HuangguoMirrorEntry(
                              item: alias,
                              source: status.source,
                              label:
                                  '重复线路 ${aliases[item.origin]!.indexOf(alias) + 1}',
                            ),
                          ),
                      ],
                    ),
                ],
              ),
            ),
          ),
        if (status.mirrorPublications.isNotEmpty) const Divider(),
        for (final publication in status.mirrorPublications)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '${publication.kindLabel} ${status.mirrorPublications.indexOf(publication) + 1}',
                  ),
                  Text(sourceProgressText(publication.message)),
                  Text('读取：${sourceTimestamp(publication.checkedAt)}'),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

class _HuangguoMirrorEntry extends StatelessWidget {
  const _HuangguoMirrorEntry({
    required this.item,
    required this.source,
    required this.label,
  });
  final SourceMirror item;
  final String source, label;

  @override
  Widget build(BuildContext context) => Align(
    alignment: Alignment.centerLeft,
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label),
        Text('${item.kindLabel} · ${item.label}'),
        Text('加入日期：${item.addedOn}'),
        Text(
          '海报：${item.poster ? "通过" : "未通过"} · '
          '浏览：${item.browse ? "通过" : "未通过"} · '
          '选集：${item.selection ? "通过" : "未通过"} · '
          '实播：${item.playback ? "通过" : "未通过"}',
        ),
        if (item.posterMessage.isNotEmpty)
          Text(sourceProgressText(item.posterMessage)),
        Text(sourceProgressText(item.message)),
        if (item.catalogMessage.isNotEmpty)
          Text(sourceProgressText('目录检查：${item.catalogMessage}')),
        if (item.lastError.isNotEmpty && item.lastError != item.message)
          Text(sourceProgressText('上次失败：${item.lastError}')),
        Text('检查：${sourceTimestamp(item.checkedAt)}'),
      ],
    ),
  );
}

class SourcesScreen extends StatefulWidget {
  const SourcesScreen({
    super.key,
    required this.repository,
    required this.store,
    this.initialSource,
    this.drama,
  });

  final AppRepository repository;
  final LocalStore store;
  final String? initialSource;
  final Drama? drama;

  @override
  State<SourcesScreen> createState() => _SourcesScreenState();
}

class _SourcesScreenState extends State<SourcesScreen> {
  final _statuses = <String, SourceStatus>{};
  Map<String, dynamic>? _soraniAccount;
  Map<String, dynamic>? _piguoAccess;
  final _errors = <String, String>{};
  final _pending = <String>{};
  final _revisions = <String, int>{};
  final _expandedHealth = <String>{};
  Timer? _timer;
  bool _polling = false;
  int _ticks = 0;
  SourcePlaybackCheck? _playbackCheck;
  VideoController? _checkVideo;
  String _checkingSource = '', _checkStage = '';
  bool _interventionOpen = false;
  final _interventionsShown = <String>{};
  final _openedAt = DateTime.now();

  List<SourceSite> get _managementSources =>
      !widget.store.locked && widget.store.profile.admin
      ? SourceSite.values.toList()
      : widget.store.sources;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      _ticks++;
      if (_statuses.values.any((status) => status.retryAt != null)) {
        setState(() {});
      }
      if (_ticks % 2 == 0 &&
          (_statuses.values.any((status) => status.running) ||
              _ticks % 10 == 0)) {
        unawaited(_refresh());
      }
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    unawaited(_playbackCheck?.cancel());
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_polling) return;
    _polling = true;
    final epoch = widget.store.profileEpoch;
    try {
      await Future.wait([
        for (final source in _managementSources)
          (() async {
            final revision = _revisions[source.id] ?? 0;
            try {
              final status = await widget.repository.sourceStatus(source.id);
              final account =
                  const {'sorani', 'piguo'}.contains(source.id) &&
                      widget.store.allowsSource(source.id)
                  ? await widget.repository.sourceAccount(source.id, 'status')
                  : null;
              if (!mounted ||
                  epoch != widget.store.profileEpoch ||
                  revision != (_revisions[source.id] ?? 0)) {
                return;
              }
              setState(() {
                _statuses[source.id] = status;
                if (source.id == 'sorani' && account != null) {
                  _soraniAccount = account;
                }
                if (source.id == 'piguo' && account != null) {
                  _piguoAccess = account;
                }
                _errors.remove(source.id);
              });
              if (SourceIntervention(status).required &&
                  status.finishedAt?.isAfter(_openedAt) == true &&
                  widget.store.allowsSource(source.id)) {
                WidgetsBinding.instance.addPostFrameCallback((_) {
                  if (mounted) {
                    unawaited(
                      _handleIntervention(source, status, automatic: true),
                    );
                  }
                });
              }
            } catch (error) {
              if (mounted &&
                  epoch == widget.store.profileEpoch &&
                  revision == (_revisions[source.id] ?? 0)) {
                setState(() => _errors[source.id] = error.toString());
              }
            }
          })(),
      ]);
    } finally {
      _polling = false;
    }
  }

  Future<void> _handleIntervention(
    SourceSite source,
    SourceStatus status, {
    bool automatic = false,
  }) async {
    final fingerprint = '${source.id}|${status.finishedAt}|${status.error}';
    if (_interventionOpen ||
        !mounted ||
        ModalRoute.of(context)?.isCurrent == false ||
        automatic && _interventionsShown.contains(fingerprint)) {
      return;
    }
    _interventionOpen = true;
    _interventionsShown.add(fingerprint);
    try {
      final resume =
          source.id == 'piguo' && !SourceIntervention(status).rateLimited
          ? await openPiguoAccess(
              context,
              repository: widget.repository,
              store: widget.store,
            )
          : await showSourceIntervention(
              context,
              name: source.name,
              status: status,
            );
      if (!resume || !mounted) return;
      final operation = switch (status.operation) {
        'more' || 'metadata' || 'vipMetadata' => status.operation,
        'check' || 'checkCatalog' => 'checkCatalog',
        _ => 'refreshAll',
      };
      await _run(source, operation);
    } finally {
      _interventionOpen = false;
    }
  }

  Future<void> _run(SourceSite source, String operation) async {
    if (operation == 'visibility') {
      final hidden = !widget.store.isSourceHidden(source.id);
      try {
        await widget.store.setSourceHidden(source.id, hidden);
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text(
                hidden
                    ? '已隐藏${source.name}，可在站源管理中取消隐藏'
                    : '已取消隐藏${source.name}',
              ),
            ),
          );
        }
      } catch (error) {
        if (mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text('未能保存隐藏设置：$error')));
        }
      }
      return;
    }
    if (_pending.contains(source.id)) return;
    final epoch = widget.store.profileEpoch;
    setState(() {
      _pending.add(source.id);
      _errors.remove(source.id);
      _revisions[source.id] = (_revisions[source.id] ?? 0) + 1;
      if (operation == 'check' || operation == 'checkCatalog') {
        _expandedHealth.add(source.id);
      }
    });
    try {
      if (operation == 'check' &&
          source.id == 'huangguoai' &&
          widget.repository.supportsRealSourcePlayback) {
        if (_playbackCheck != null) throw AppFailure('请等待当前站源实播检查完成');
        MediaKit.ensureInitialized();
        _checkingSource = source.id;
        final checker = SourcePlaybackCheck(
          widget.repository,
          onVideo: (controller) {
            if (mounted) setState(() => _checkVideo = controller);
          },
          onStage: (stage) {
            if (mounted) setState(() => _checkStage = stage);
          },
        );
        _playbackCheck = checker;
        await checker.run(source);
        if (mounted && epoch == widget.store.profileEpoch) {
          final status = await widget.repository.sourceStatus(source.id);
          if (mounted) setState(() => _statuses[source.id] = status);
        }
        return;
      }
      final status = operation == 'cancel'
          ? await widget.repository.cancelSourceJob(source.id)
          : await widget.repository.startSourceJob(
              source.id,
              operation,
              drama: source.id == widget.drama?.source ? widget.drama : null,
            );
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _statuses[source.id] = status);
      }
    } catch (error) {
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _errors[source.id] = error.toString());
      }
    } finally {
      if (_checkingSource == source.id) {
        _playbackCheck = null;
        _checkingSource = '';
        _checkStage = '';
      }
      if (mounted) setState(() => _pending.remove(source.id));
    }
  }

  Future<void> _copy(SourceSite source, SourceStatus status) async {
    final text = StringBuffer('${source.name}\n');
    text.writeln(
      '缓存 ${status.count} 部，更新 ${sourceTimestamp(status.updatedAt)}',
    );
    final health = status.health;
    if (health != null) {
      text.writeln('${health.label} · ${sourceTimestamp(health.checkedAt)}');
      if (health.networkMode.isNotEmpty) {
        text.writeln('检测网络模式：${health.networkMode}');
      }
      if (health.sample.isNotEmpty) text.writeln('检测剧集：${health.sample}');
      for (final step in health.steps) {
        text.writeln('${step.name}：${step.message}');
        text.writeln('HTTP ${step.httpStatus} · ${step.elapsedMs} ms');
        if (step.cfRay.isNotEmpty) text.writeln('CF Ray: ${step.cfRay}');
      }
    }
    if (health?.downloadCheck != null) {
      text.writeln(
        '${health!.downloadCheck!.name}：${health.downloadCheck!.message}',
      );
    }
    for (final mirror in status.mirrors) {
      text.writeln('${mirror.origin} · ${mirror.kindLabel} · ${mirror.label}');
      text.writeln('加入：${mirror.addedOn} · ${mirror.addedFrom}');
      text.writeln('海报：${mirror.posterMessage}');
      if (mirror.duplicateOf.isNotEmpty) {
        text.writeln('已合并到：${mirror.duplicateOf}');
      }
      text.writeln(mirror.message);
      if (mirror.effectiveOrigin.isNotEmpty) {
        text.writeln('实际落点：${mirror.effectiveOrigin}');
      }
      if (mirror.lastError.isNotEmpty) text.writeln('上次失败：${mirror.lastError}');
    }
    for (final publication in status.mirrorPublications) {
      text.writeln('${publication.url}：${publication.message}');
    }
    if (status.error.isNotEmpty) text.writeln(status.error);
    if (status.storageError.isNotEmpty) text.writeln(status.storageError);
    await Clipboard.setData(
      ClipboardData(text: sourceProgressText(text.toString())),
    );
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('诊断信息已复制')));
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (context, _) {
      final sources = _managementSources
        ..sort((a, b) {
          final aFirst = a.id == widget.initialSource ? 0 : 1;
          final bFirst = b.id == widget.initialSource ? 0 : 1;
          return aFirst.compareTo(bFirst);
        });
      final viewPaddingBottom = MediaQuery.viewPaddingOf(context).bottom;
      final paddingBottom = MediaQuery.paddingOf(context).bottom;
      final bottomInset = viewPaddingBottom > paddingBottom
          ? viewPaddingBottom
          : paddingBottom;
      return Scaffold(
        appBar: AppBar(title: const Text('站源管理')),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 960),
            child: RefreshIndicator(
              onRefresh: _refresh,
              child: ListView(
                key: const ValueKey('source-manager-list'),
                padding: EdgeInsets.fromLTRB(16, 16, 16, 16 + bottomInset),
                children: [
                  const Padding(
                    padding: EdgeInsets.only(bottom: 16),
                    child: Text(
                      '线路地址仅记入 logs/app.log，实际日志位置可在设置 → 网络与资源中查看。保留全部站源及失败记录；更多操作可隐藏或取消隐藏，按当前用户保存，不删除数据或改变权限。未解锁站源仍需授权。Windows 检测会联网读取目录、搜索、选集并短暂实播，不写观看历史；离开此页停止实播。',
                    ),
                  ),
                  if (sources.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(24),
                      child: Text('当前用户没有可用站源'),
                    ),
                  for (final source in sources) _sourceCard(source),
                ],
              ),
            ),
          ),
        ),
      );
    },
  );

  Widget _sourceCard(SourceSite source) {
    final status = _statuses[source.id];
    final pending = _pending.contains(source.id);
    final busy = pending || status?.running == true;
    final seconds = status?.retrySeconds ?? 0;
    final enabled =
        !busy &&
        seconds == 0 &&
        widget.repository.supportsSourceManagement &&
        widget.store.allowsSource(source.id) &&
        _playbackCheck == null;
    final rawError = _errors[source.id] ?? status?.error ?? '';
    final error = sourceProgressText(
      source.id == 'sorani'
          ? soraniSourceError(
              rawError,
              authenticated: _soraniAccount?['authenticated'] == true,
            )
          : rawError,
    );
    final health = status?.health;
    final healthExpanded = _expandedHealth.contains(source.id);
    final colors = Theme.of(context).colorScheme;
    return Card(
      key: ValueKey('source-${source.id}'),
      margin: const EdgeInsets.only(bottom: 16),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.dns_outlined),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    source.name,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                Text('${status?.count ?? 0} 部'),
              ],
            ),
            const SizedBox(height: 8),
            if (widget.store.isSourceHidden(source.id))
              const Text('已隐藏 · 前端不展示，可在更多操作中取消隐藏'),
            if (!widget.store.allowsSource(source.id))
              const Text('未解锁 · 站源和检测记录保留，解锁后才能更新、搜索与播放'),
            Text('最近更新：${sourceTimestamp(status?.updatedAt)}'),
            if (status != null && status.count > 0)
              Text(
                '已加载至第 ${status.page} 页${status.hasMore ? ' · 可继续加载' : ' · 当前分页已加载完'}',
              ),
            if (status != null && status.catalogRule.isNotEmpty)
              Text(
                sourceProgressText(
                  '采集规则：${status.catalogRule} · 本次 ${status.catalogPages} 页 / ${status.catalogFetched} 部',
                ),
              ),
            if (status != null && status.catalogOrigin.isNotEmpty) ...[
              Text('最近完整同步：${sourceTimestamp(status.catalogFullAt)}'),
            ],
            if (source.id != 'huangguoai')
              const Text('本地无匹配后再查线上；线路只检测连接，实播请手动确认'),
            if (source.id == 'piguo')
              Text(
                _piguoAccess?['authenticated'] == true
                    ? '皮果浏览器验证已连接'
                    : '皮果若返回浏览器验证，请在内置原站页手动完成后连接',
              ),
            const SizedBox(height: 12),
            if (source.id == 'sorani' && widget.store.allowsSource(source.id))
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text(
                  _soraniAccount?['authenticated'] == true
                      ? '青空账号已连接：${_soraniAccount?['name'] ?? ''}'
                      : '青空需要登录；没有账号请先到源站自行注册，再登录账号播放。',
                ),
              ),
            Wrap(
              spacing: 10,
              runSpacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                if (source.id == 'sorani')
                  OutlinedButton.icon(
                    key: const ValueKey('login-sorani'),
                    onPressed:
                        !busy &&
                            widget.repository.supportsSourceManagement &&
                            widget.store.allowsSource(source.id)
                        ? () async {
                            await openSoraniLogin(
                              context,
                              repository: widget.repository,
                              store: widget.store,
                            );
                            if (mounted) await _refresh();
                          }
                        : null,
                    icon: const Icon(Icons.person_outline),
                    label: Text(
                      _soraniAccount?['authenticated'] == true
                          ? '青空账号管理'
                          : '登录青空账号',
                    ),
                  ),
                if (source.id == 'piguo')
                  OutlinedButton.icon(
                    key: const ValueKey('access-piguo'),
                    onPressed:
                        !busy &&
                            widget.repository.supportsSourceManagement &&
                            widget.store.allowsSource(source.id)
                        ? () async {
                            final connected = await openPiguoAccess(
                              context,
                              repository: widget.repository,
                              store: widget.store,
                            );
                            if (connected && mounted) {
                              await _run(source, 'checkCatalog');
                            } else if (mounted) {
                              await _refresh();
                            }
                          }
                        : null,
                    icon: const Icon(Icons.verified_user_outlined),
                    label: Text(
                      _piguoAccess?['authenticated'] == true
                          ? '皮果验证管理'
                          : '连接皮果浏览器验证',
                    ),
                  ),
                FilledButton.icon(
                  key: ValueKey('update-${source.id}'),
                  onPressed: enabled ? () => _run(source, 'update') : null,
                  icon: const Icon(Icons.sync_rounded),
                  label: Text(source.id == 'huangguoai' ? '更新' : '按规则重新采集'),
                ),
                OutlinedButton.icon(
                  key: ValueKey('check-${source.id}'),
                  onPressed: enabled ? () => _run(source, 'check') : null,
                  icon: const Icon(Icons.network_check),
                  label: Text(
                    source.id == 'huangguoai' &&
                            widget.repository.supportsRealSourcePlayback
                        ? '检测真实浏览、搜索与播放'
                        : '检测浏览、搜索与线路连接',
                  ),
                ),
                if (_checkingSource == source.id)
                  TextButton(
                    onPressed: () => _playbackCheck?.cancel(),
                    child: const Text('停止实播检查'),
                  ),
                if (status?.running == true)
                  TextButton(
                    onPressed: pending ? null : () => _run(source, 'cancel'),
                    child: const Text('停止'),
                  ),
                if (status != null && SourceIntervention(status).required)
                  OutlinedButton.icon(
                    key: ValueKey('source-human-${source.id}'),
                    onPressed: !busy && widget.store.allowsSource(source.id)
                        ? () => _handleIntervention(source, status)
                        : null,
                    icon: const Icon(Icons.verified_user_outlined),
                    label: Text(
                      SourceIntervention(status).rateLimited
                          ? '限流等待与继续'
                          : '人工验证后继续',
                    ),
                  ),
                PopupMenuButton<String>(
                  key: ValueKey('source-more-${source.id}'),
                  tooltip: '${source.name}更多操作',
                  enabled: !widget.store.locked,
                  onSelected: (operation) => _run(source, operation),
                  itemBuilder: (_) => [
                    PopupMenuItem(
                      value: 'visibility',
                      enabled:
                          widget.store.profile.admin ||
                          widget.store.allowsSource(source.id),
                      child: Text(
                        widget.store.isSourceHidden(source.id) ? '取消隐藏' : '隐藏',
                      ),
                    ),
                    PopupMenuItem(
                      value: 'more',
                      enabled: enabled && (status?.hasMore ?? true),
                      child: const Text('继续加载一页'),
                    ),
                    PopupMenuItem(
                      value: 'metadata',
                      enabled: enabled,
                      child: const Text('补齐资料'),
                    ),
                    if (source.id == 'huangdou')
                      PopupMenuItem(
                        value: 'vipMetadata',
                        enabled: enabled && (status?.unknownVip ?? 0) > 0,
                        child: Text('补齐 VIP 资料（${status?.unknownVip ?? 0} 部）'),
                      ),
                    PopupMenuItem(
                      value: 'checkCatalog',
                      enabled: enabled,
                      child: const Text('仅检测目录'),
                    ),
                    if (source.id == 'huangguoai' ||
                        source.id == 'huangguo-video')
                      PopupMenuItem(
                        value: 'checkMirrors',
                        enabled: enabled,
                        child: const Text('检测黄果备用地址'),
                      ),
                  ],
                ),
              ],
            ),
            if (_checkingSource == source.id && _checkVideo != null)
              SizedBox(
                height: 180,
                child: Video(
                  controller: _checkVideo!,
                  controls: NoVideoControls,
                ),
              ),
            if (_checkingSource == source.id)
              Text(sourceProgressText(_checkStage)),
            if (busy) ...[
              const SizedBox(height: 12),
              LinearProgressIndicator(
                value: status != null && status.total > 0
                    ? (status.completed / status.total).clamp(0, 1)
                    : null,
              ),
              const SizedBox(height: 8),
              Text(
                '${status?.displayStage ?? '准备中'}${(status?.total ?? 0) > 0 ? ' · ${status!.completed}/${status.total}' : ''}',
              ),
            ] else if (status != null && status.stage.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(
                '上次目录任务：${status.displayStage}${status.added > 0 ? ' · 新增 ${status.added} 部' : ''}',
              ),
            ],
            if (seconds > 0)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  '请在 $seconds 秒后重试',
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: SelectableText(
                  error,
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (status != null && status.storageError.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      sourceProgressText(status.storageError),
                      style: TextStyle(color: colors.error),
                    ),
                    TextButton.icon(
                      key: ValueKey('save-${source.id}'),
                      onPressed:
                          !busy && widget.repository.supportsSourceManagement
                          ? () => _run(source, 'retrySave')
                          : null,
                      icon: const Icon(Icons.save_outlined),
                      label: const Text('重试保存'),
                    ),
                  ],
                ),
              ),
            if (status != null &&
                (status.mirrors.isNotEmpty ||
                    status.mirrorPublications.isNotEmpty))
              HuangguoMirrorsPanel(status: status),
            if (status != null && status.healthHistory.isNotEmpty)
              ExpansionTile(
                key: ValueKey('source-history-${source.id}'),
                title: Text('历史检测（${status.healthHistory.length}）'),
                subtitle: const Text('保留成功、失败及不同网络模式的结果'),
                children: [
                  for (final previous in status.healthHistory.reversed)
                    ListTile(
                      title: Text(
                        '${sourceTimestamp(previous.checkedAt)} · ${previous.networkMode} · ${previous.label}',
                      ),
                      subtitle: Text(
                        sourceProgressText(
                          [
                            for (final step in previous.steps)
                              '${step.name} [${step.state}]：${step.message}',
                            if (previous.downloadCheck != null)
                              '${previous.downloadCheck!.name} [${previous.downloadCheck!.state}]：${previous.downloadCheck!.message}',
                          ].join('\n'),
                        ),
                      ),
                    ),
                ],
              ),
            if (health != null) ...[
              const Divider(height: 28),
              if (health.downloadCheck != null)
                SelectableText(
                  sourceProgressText(
                    '${health.downloadCheck!.name} [${health.downloadCheck!.state}]：${health.downloadCheck!.message}',
                  ),
                ),
              if (health.networkMode.isNotEmpty)
                Text(sourceProgressText('检测网络模式：${health.networkMode}')),
              Row(
                children: [
                  Expanded(
                    child: Semantics(
                      expanded: healthExpanded,
                      child: Tooltip(
                        message: healthExpanded ? '收起检测详情' : '展开检测详情',
                        child: TextButton(
                          key: ValueKey('health-toggle-${source.id}'),
                          onPressed: () => setState(() {
                            if (healthExpanded) {
                              _expandedHealth.remove(source.id);
                            } else {
                              _expandedHealth.add(source.id);
                            }
                          }),
                          style: TextButton.styleFrom(
                            foregroundColor: colors.onSurface,
                            padding: const EdgeInsets.symmetric(
                              horizontal: 4,
                              vertical: 8,
                            ),
                          ),
                          child: Row(
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(health.label),
                                    Text(
                                      sourceTimestamp(health.checkedAt),
                                      style: Theme.of(
                                        context,
                                      ).textTheme.bodySmall,
                                    ),
                                  ],
                                ),
                              ),
                              const SizedBox(width: 8),
                              Icon(
                                healthExpanded
                                    ? Icons.expand_less_rounded
                                    : Icons.expand_more_rounded,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                  IconButton(
                    tooltip: '复制诊断信息',
                    onPressed: () => _copy(source, status!),
                    icon: const Icon(Icons.copy_rounded),
                  ),
                ],
              ),
              if (healthExpanded) ...[
                if (health.sample.isNotEmpty)
                  Text(sourceProgressText('检测剧集：${health.sample}')),
                for (final step in health.steps)
                  Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Icon(
                          step.state == 'ok'
                              ? Icons.check_circle_outline
                              : Icons.error_outline,
                          size: 20,
                          color: step.state == 'ok'
                              ? colors.primary
                              : colors.error,
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                sourceProgressText(
                                  '${step.name}：${step.message}',
                                ),
                              ),
                              if (step.host.isNotEmpty || step.httpStatus > 0)
                                Text(
                                  '${step.httpStatus > 0 ? 'HTTP ${step.httpStatus} · ' : ''}${step.elapsedMs} ms',
                                  style: Theme.of(context).textTheme.bodySmall,
                                ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ] else ...[
              const SizedBox(height: 12),
              const Text('尚未检测连接'),
            ],
          ],
        ),
      ),
    );
  }
}

class SourceDiagnosticsButton extends StatelessWidget {
  const SourceDiagnosticsButton({
    super.key,
    required this.repository,
    required this.store,
    required this.drama,
  });
  final AppRepository repository;
  final LocalStore store;
  final Drama drama;

  @override
  Widget build(BuildContext context) => TextButton.icon(
    onPressed: () => Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => SourcesScreen(
          repository: repository,
          store: store,
          initialSource: drama.source,
          drama: drama,
        ),
      ),
    ),
    icon: const Icon(Icons.network_check),
    label: const Text('站源诊断'),
  );
}
