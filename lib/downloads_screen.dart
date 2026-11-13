import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_layout.dart';
import 'core_bridge.dart';
import 'download_collections.dart';
import 'download_folders.dart';
import 'local_store.dart';
import 'local_media_screen.dart';
import 'models.dart';
import 'player_screen.dart';
import 'resource_settings_screen.dart';
import 'settings_screen.dart';
import 'widgets.dart';

class DownloadsScreen extends StatefulWidget {
  const DownloadsScreen({
    super.key,
    required this.repository,
    required this.store,
    this.embedded = false,
    this.playerBuilder,
    this.folderOpener = const DownloadFolderOpener(),
  });
  final AppRepository repository;
  final LocalStore store;
  final bool embedded;
  final DownloadFolderOpener folderOpener;
  @visibleForTesting
  final Widget Function(DramaDetail, int, double)? playerBuilder;
  @override
  State<DownloadsScreen> createState() => _DownloadsScreenState();
}

class _DownloadsScreenState extends State<DownloadsScreen> {
  Timer? _timer;
  final _search = TextEditingController();
  final _selected = <String>{};
  final _expanded = <String>{};
  late final DownloadCollectionUpdater _updater;
  late final int _epoch;
  List<DownloadJob> _jobs = [];
  bool _loading = true,
      _refreshing = false,
      _opening = false,
      _openingFolder = false,
      _selecting = false,
      _busy = false,
      _stopBatch = false;
  String? _error, _retryCommand, _openingJob;
  String _filter = 'all', _release = '', _operation = '';
  bool get _allowed =>
      widget.store.profileEpoch == _epoch && widget.store.canDownload;
  List<DownloadCollection> get _collections => downloadCollections(
    _jobs,
    query: _search.text,
    filter: _filter,
    release: _release,
  );

  @override
  void initState() {
    super.initState();
    _epoch = widget.store.profileEpoch;
    _updater = DownloadCollectionUpdater(widget.repository, widget.store)
      ..addListener(_changed);
    widget.store.addListener(_changed);
    _refresh();
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _refresh());
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _stopBatch = true;
    _timer?.cancel();
    _search.dispose();
    widget.store.removeListener(_changed);
    _updater.removeListener(_changed);
    _updater.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_refreshing || _opening || !_allowed) return;
    _refreshing = true;
    try {
      final jobs = await widget.repository.downloads();
      if (mounted && _allowed) {
        setState(() {
          _jobs = jobs;
          _selected.retainAll(jobs.map((job) => job.id));
          _error = null;
        });
      }
    } catch (error) {
      if (mounted && _allowed) setState(() => _error = error.toString());
    } finally {
      _refreshing = false;
      if (mounted) setState(() => _loading = false);
    }
  }

  void _message(String message) {
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
    }
  }

  Future<void> _openFolder(Iterable<DownloadJob> jobs) async {
    if (_openingFolder || !_allowed) return;
    setState(() => _openingFolder = true);
    try {
      final directory = await widget.folderOpener.resolve(
        widget.repository,
        jobs.where((job) => widget.store.allowsSource(job.drama.source)),
      );
      if (!mounted || !_allowed) return;
      await widget.folderOpener.open(directory);
    } catch (error) {
      if (mounted && _allowed) _message(error.toString());
    } finally {
      if (mounted) setState(() => _openingFolder = false);
    }
  }

  Future<void> _batch(String command, Iterable<DownloadJob> source) async {
    if (_busy || !_allowed) return;
    final jobs = source
        .where(
          (job) => switch (command) {
            'pause' => job.active,
            'resume' => job.resumable,
            'archive' => job.completed && !job.archived,
            'restore' => job.completed && job.archived,
            'remove' => true,
            _ => false,
          },
        )
        .toList();
    if (jobs.isEmpty) {
      _message('没有适合此操作的任务');
      return;
    }
    final selectedIds = jobs.map((job) => job.id).toSet();
    final wholeCollections = <String, Drama>{};
    if (command == 'remove') {
      final partial = {
        for (final job in _jobs)
          if (!selectedIds.contains(job.id)) job.drama.id,
      };
      for (final job in jobs) {
        if (!partial.contains(job.drama.id)) {
          wholeCollections[job.drama.id] = job.drama;
        }
      }
    }
    if (command == 'remove') {
      final completed = jobs.where((job) => job.completed).length;
      final accepted = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(completed > 0 ? '删除所选视频？' : '取消所选下载？'),
          content: Text(
            '共 ${jobs.length} 项${completed > 0 ? '，包含 $completed 个已下载视频' : ''}，文件也会删除。'
            '${wholeCollections.isEmpty ? '' : '\n整部合集还会删除该剧合并成品及导出的特别篇，并清理对应空目录。'}'
            '\n需要保留视频时，请选择“清理任务，保留视频”。',
          ),
          actions: [
            TextButton(
              autofocus: AppLayout.isTelevision(context),
              onPressed: () => Navigator.pop(context, false),
              child: const Text('保留'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认删除'),
            ),
          ],
        ),
      );
      if (accepted != true || !mounted || !_allowed) return;
    }
    setState(() {
      _busy = true;
      _stopBatch = false;
      _retryCommand = null;
    });
    final failures = <String, String>{};
    var completed = 0;
    try {
      final handled = <String>{};
      for (final drama in wholeCollections.values) {
        if (!mounted || !_allowed || _stopBatch) break;
        final ids = jobs
            .where((job) => job.drama.id == drama.id)
            .map((job) => job.id)
            .toList();
        handled.addAll(ids);
        setState(() => _operation = '正在删除合集及本地成品：${drama.title}');
        try {
          final result = await widget.repository.removeDownloadCollection(
            drama,
            ids,
          );
          if (!mounted || !_allowed) return;
          completed += result.completed.length;
          _selected.removeAll(result.completed);
          failures.addAll(result.failures);
        } catch (error) {
          failures.addAll({for (final id in ids) id: error.toString()});
        }
      }
      final remaining = jobs.where((job) => !handled.contains(job.id)).toList();
      for (var offset = 0; offset < remaining.length; offset += 500) {
        if (!mounted || !_allowed || _stopBatch) break;
        setState(
          () => _operation =
              '正在处理 ${math.min(offset + 500, remaining.length)}/${remaining.length} 项',
        );
        final ids = remaining
            .sublist(offset, math.min(offset + 500, remaining.length))
            .map((job) => job.id)
            .toList();
        try {
          final result = await widget.repository.controlDownloadBatch(
            command,
            ids,
          );
          if (!mounted || !_allowed) return;
          completed += result.completed.length;
          _selected.removeAll(result.completed);
          failures.addAll(result.failures);
        } catch (error) {
          failures.addAll({for (final id in ids) id: error.toString()});
        }
      }
      if (!mounted || !_allowed) return;
      if (failures.isNotEmpty) {
        _selected.addAll(failures.keys);
        _selecting = true;
        _retryCommand = command;
        _message(
          '已处理 $completed 项，${failures.length} 项失败：${failures.values.first}',
        );
      } else {
        _message(
          _stopBatch
              ? '已停止，已处理 $completed 项'
              : command == 'archive'
              ? '已清理 $completed 项任务，视频仍可本地播放'
              : '已处理 $completed 项',
        );
      }
      await _refresh();
    } finally {
      if (mounted) {
        setState(() {
          _busy = false;
          _operation = '';
        });
      }
    }
  }

  Future<void> _update(Drama drama) async {
    await _updater.update(drama);
    if (mounted && _allowed) {
      _message(_updater.error ?? _updater.status);
      await _refresh();
    }
  }

  Future<void> _play(DownloadJob job) async {
    if (_opening || !_allowed) return;
    setState(() {
      _opening = true;
      _openingJob = job.id;
    });
    final completed =
        _jobs
            .where((entry) => entry.completed && entry.drama.id == job.drama.id)
            .toList()
          ..sort((a, b) => a.episode.number.compareTo(b.episode.number));
    final index = completed.indexWhere((entry) => entry.id == job.id);
    if (index < 0) {
      setState(() {
        _opening = false;
        _openingJob = null;
      });
      return;
    }
    final detail = DramaDetail(
      job.drama,
      completed.map((entry) => entry.episode).toList(),
    );
    final watch = widget.store.watched(job.drama.id);
    final position = watch?.episode == job.episode.number && !watch!.finished
        ? watch.position
        : 0.0;
    try {
      await WidgetsBinding.instance.endOfFrame;
      await Future<void>.delayed(Duration.zero);
      if (!mounted || !_allowed) return;
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) =>
              widget.playerBuilder?.call(detail, index, position) ??
              PlayerScreen(
                detail: detail,
                initialIndex: index,
                initialPosition: position,
                repository: widget.repository,
                store: widget.store,
                localOnly: true,
              ),
        ),
      );
    } finally {
      _opening = false;
      if (mounted) {
        setState(() => _openingJob = null);
        await _refresh();
      }
    }
  }

  void _select(Iterable<DownloadJob> jobs) {
    final ids = jobs.map((job) => job.id).toSet();
    setState(() {
      _selecting = true;
      if (ids.every(_selected.contains)) {
        _selected.removeAll(ids);
      } else {
        _selected.addAll(ids);
      }
    });
  }

  Future<void> _filters() async {
    var state = _filter, release = _release;
    final result = await showDialog<bool>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, change) => AlertDialog(
          title: const Text('筛选下载合集'),
          content: SizedBox(
            width: 420,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('任务状态'),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final entry in const {
                        'all': '全部任务',
                        'pending': '未完成',
                        'active': '进行中',
                        'paused': '已暂停',
                        'failed': '失败',
                        'completed': '已下载',
                        'archived': '已保留视频',
                      }.entries)
                        ChoiceChip(
                          label: Text(entry.value),
                          selected: state == entry.key,
                          onSelected: (_) => change(() => state = entry.key),
                        ),
                    ],
                  ),
                  const SizedBox(height: 20),
                  const Text('剧集状态'),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final entry in const {
                        '': '全部',
                        'ongoing': '连载中',
                        'finished': '已完结',
                      }.entries)
                        ChoiceChip(
                          label: Text(entry.value),
                          selected: release == entry.key,
                          onSelected: (_) => change(() => release = entry.key),
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('应用'),
            ),
          ],
        ),
      ),
    );
    if (result == true && mounted) {
      setState(() {
        _filter = state;
        _release = release;
      });
    }
  }

  List<Widget> _titleActions(List<DownloadCollection> collections) => [
    if (_selecting) ...[
      IconButton(
        tooltip: '全选当前',
        onPressed: _busy
            ? null
            : () => _select(collections.expand((c) => c.jobs)),
        icon: const Icon(Icons.select_all_rounded),
      ),
      IconButton(
        tooltip: '退出多选',
        onPressed: _busy
            ? null
            : () => setState(() {
                _selecting = false;
                _selected.clear();
                _retryCommand = null;
              }),
        icon: const Icon(Icons.close_rounded),
      ),
    ] else ...[
      IconButton(
        key: const ValueKey('download-filters'),
        tooltip: '筛选下载合集',
        onPressed: _filters,
        icon: Badge(
          isLabelVisible: _filter != 'all' || _release.isNotEmpty,
          child: const Icon(Icons.filter_list_rounded),
        ),
      ),
      IconButton(
        key: const ValueKey('download-select-tasks'),
        tooltip: '多选下载任务',
        onPressed: () => setState(() => _selecting = true),
        icon: const Icon(Icons.checklist_rounded),
      ),
      IconButton(
        key: const ValueKey('download-local-media'),
        tooltip: '本地媒体与合并',
        onPressed: () => Navigator.push(
          context,
          MaterialPageRoute<void>(
            builder: (_) => LocalMediaScreen(
              repository: widget.repository,
              store: widget.store,
            ),
          ),
        ),
        icon: const Icon(Icons.video_library_outlined),
      ),
    ],
    if (widget.store.profile.admin)
      IconButton(
        key: const ValueKey('download-queue-settings'),
        tooltip: '排队与下载限速',
        onPressed: _busy || !_allowed
            ? null
            : () => Navigator.push(
                context,
                MaterialPageRoute<void>(
                  builder: (_) => ResourceSettingsScreen(
                    repository: widget.repository,
                    store: widget.store,
                  ),
                ),
              ),
        icon: const Icon(Icons.tune_rounded),
      ),
    IconButton(
      key: const ValueKey('download-queue-pause'),
      tooltip: '全部暂停',
      onPressed: _busy || !_allowed ? null : () => _batch('pause', _jobs),
      icon: const Icon(Icons.pause_circle_outline_rounded),
    ),
    IconButton(
      key: const ValueKey('download-queue-resume'),
      tooltip: '全部继续',
      onPressed: _busy || !_allowed ? null : () => _batch('resume', _jobs),
      icon: const Icon(Icons.play_circle_outline_rounded),
    ),
    IconButton(
      key: const ValueKey('download-queue-archive'),
      tooltip: '清理已完成任务，保留视频',
      onPressed: _busy || !_allowed ? null : () => _batch('archive', _jobs),
      icon: const Icon(Icons.playlist_remove_rounded),
    ),
    IconButton(
      key: const ValueKey('download-queue-refresh'),
      tooltip: '刷新记录',
      onPressed: _busy || !_allowed ? null : _refresh,
      icon: const Icon(Icons.refresh_rounded),
    ),
  ];

  Widget _collection(DownloadCollection collection) {
    final count = collection.jobs
        .where((job) => _selected.contains(job.id))
        .length;
    final expanded = _expanded.contains(collection.drama.id);
    return Card(
      child: ListTile(
        leading: _selecting
            ? Checkbox(
                tristate: true,
                value: count == 0
                    ? false
                    : count == collection.jobs.length
                    ? true
                    : null,
                onChanged: _busy ? null : (_) => _select(collection.jobs),
              )
            : Icon(expanded ? Icons.folder_open_rounded : Icons.folder_rounded),
        title: Text(
          collection.drama.title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(fontWeight: FontWeight.w600),
        ),
        subtitle: Text(
          '${collection.completed}/${collection.jobs.length} 集已下载 · ${storageSize(collection.bytes)}'
          '${collection.active > 0 ? ' · ${collection.active} 项进行中' : ''}${collection.failed > 0 ? ' · ${collection.failed} 项失败' : ''}',
        ),
        onTap: () => setState(() {
          if (!_expanded.remove(collection.drama.id)) {
            _expanded.add(collection.drama.id);
          }
        }),
        trailing: PopupMenuButton<String>(
          tooltip: '${collection.drama.title} · 合集操作',
          enabled: !_busy,
          onSelected: (value) {
            if (value == 'folder') {
              _openFolder(collection.jobs);
            } else if (value == 'update') {
              _update(collection.drama);
            } else if (value == 'select') {
              _select(collection.jobs);
            } else {
              _batch(
                value,
                value == 'remove'
                    ? _jobs.where((job) => job.drama.id == collection.drama.id)
                    : collection.jobs,
              );
            }
          },
          itemBuilder: (_) => [
            if (widget.folderOpener.supported)
              PopupMenuItem(
                key: ValueKey(
                  'download-collection-open-folder-${collection.drama.id}',
                ),
                value: 'folder',
                enabled: !_openingFolder && collection.completed > 0,
                child: const Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.folder_open_outlined),
                    SizedBox(width: 8),
                    Text('打开已下载文件夹'),
                  ],
                ),
              ),
            PopupMenuItem(
              value: 'update',
              enabled: !_updater.busy,
              child: const Text('更新本剧 · 补充新增与缺失'),
            ),
            const PopupMenuItem(value: 'select', child: Text('选择本合集')),
            const PopupMenuItem(value: 'pause', child: Text('暂停本合集')),
            const PopupMenuItem(value: 'resume', child: Text('继续 / 重试本合集')),
            const PopupMenuItem(value: 'archive', child: Text('清理已完成任务，保留视频')),
            if (_filter == 'archived')
              const PopupMenuItem(value: 'restore', child: Text('恢复到任务列表')),
            const PopupMenuItem(value: 'remove', child: Text('取消任务并删除视频')),
          ],
        ),
      ),
    );
  }

  Widget _episode(DownloadJob job) => Padding(
    padding: const EdgeInsets.only(left: 12),
    child: Card(
      key: ValueKey('download-task-${job.id}'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ListTile(
            leading: _selecting
                ? Checkbox(
                    value: _selected.contains(job.id),
                    onChanged: _busy ? null : (_) => _select([job]),
                  )
                : _openingJob == job.id
                ? const SizedBox.square(
                    dimension: 24,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Icon(
                    job.completed
                        ? Icons.offline_pin_outlined
                        : job.state == 'failed'
                        ? Icons.error_outline
                        : Icons.downloading_rounded,
                  ),
            title: Text(
              '第 ${job.episode.number} 集${job.episode.vip ? ' · VIP' : ''}',
            ),
            subtitle: Text(
              '${_openingJob == job.id
                  ? '正在打开本地视频'
                  : job.archived
                  ? '已保留视频'
                  : job.stateLabel} · ${storageSize(job.bytes)}'
              '${job.actualQuality > 0 ? ' · ${job.actualQuality}P' : ''}${job.error.isNotEmpty ? '\n${job.error}' : ''}',
            ),
            onTap: _busy
                ? null
                : _selecting
                ? () => _select([job])
                : job.completed
                ? () => _play(job)
                : null,
            trailing: _selecting
                ? null
                : PopupMenuButton<String>(
                    enabled: !_busy,
                    tooltip: '分集操作',
                    onSelected: (value) {
                      if (value == 'play') {
                        _play(job);
                      } else if (value == 'folder') {
                        _openFolder([job]);
                      } else {
                        _batch(value, [job]);
                      }
                    },
                    itemBuilder: (_) => [
                      if (job.completed)
                        const PopupMenuItem(value: 'play', child: Text('本地播放')),
                      if (job.completed && widget.folderOpener.supported)
                        PopupMenuItem(
                          key: ValueKey(
                            'download-episode-open-folder-${job.id}',
                          ),
                          value: 'folder',
                          enabled: !_openingFolder,
                          child: const Text('打开已下载文件夹'),
                        ),
                      if (job.active)
                        const PopupMenuItem(value: 'pause', child: Text('暂停')),
                      if (job.resumable)
                        const PopupMenuItem(
                          value: 'resume',
                          child: Text('继续 / 重试'),
                        ),
                      if (job.completed)
                        PopupMenuItem(
                          value: job.archived ? 'restore' : 'archive',
                          child: Text(job.archived ? '恢复任务' : '清理任务，保留视频'),
                        ),
                      PopupMenuItem(
                        value: 'remove',
                        child: Text(job.completed ? '删除视频' : '取消下载'),
                      ),
                    ],
                  ),
          ),
          if (!job.completed)
            LinearProgressIndicator(
              value: job.progress > 0 || !job.active ? job.progress : null,
              minHeight: 2,
            ),
        ],
      ),
    ),
  );

  Widget _batchBar() => Material(
    color: Theme.of(context).colorScheme.surfaceContainerHighest,
    child: SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.all(8),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          alignment: WrapAlignment.center,
          children: [
            if (_busy)
              TextButton(
                onPressed: () => setState(() => _stopBatch = true),
                child: const Text('停止批量操作'),
              )
            else ...[
              for (final entry in const {
                'pause': '暂停',
                'resume': '继续 / 重试',
                'archive': '保留视频',
                'remove': '删除',
              }.entries)
                TextButton(
                  onPressed: _selected.isEmpty
                      ? null
                      : () => _batch(
                          entry.key,
                          _jobs.where((job) => _selected.contains(job.id)),
                        ),
                  child: Text(entry.value),
                ),
              if (_retryCommand != null)
                FilledButton(
                  onPressed: () => _batch(
                    _retryCommand!,
                    _jobs.where((job) => _selected.contains(job.id)),
                  ),
                  child: const Text('重试失败项'),
                ),
            ],
          ],
        ),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) =>
        _buildContent(context, constraints.maxWidth),
  );

  Widget _buildContent(BuildContext context, double width) {
    if (!_allowed) {
      return const StatusPanel(title: '当前用户无权访问下载', message: '请返回后重新操作。');
    }
    final collections = _collections;
    final rows = <Object>[
      for (final collection in collections) ...[
        collection,
        if (_expanded.contains(collection.drama.id)) ...collection.jobs,
      ],
    ];
    final title = _selecting ? '已选 ${_selected.length} 项' : '下载合集';
    final actions = _titleActions(collections);
    final scale = MediaQuery.textScalerOf(context).scale(20) / 20;
    final compact = width < 640 * math.max(1, scale);
    final toolbar = Align(
      alignment: Alignment.centerRight,
      child: Wrap(
        key: const ValueKey('download-toolbar-wrap'),
        alignment: WrapAlignment.end,
        children: actions,
      ),
    );
    final heading = Text(
      title,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: Theme.of(context).textTheme.titleLarge,
    );
    final body = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (widget.embedded)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 4, 0),
            child: compact
                ? Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [heading, toolbar],
                  )
                : Row(
                    children: [
                      Expanded(child: heading),
                      ...actions,
                    ],
                  ),
          ),
        if (!widget.embedded && compact)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: toolbar,
          ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
          child: TextField(
            controller: _search,
            onChanged: (_) => setState(() {}),
            decoration: InputDecoration(
              hintText: '搜索剧名、站源或分类',
              prefixIcon: const Icon(Icons.search_rounded),
              suffixIcon: _search.text.isEmpty
                  ? null
                  : IconButton(
                      tooltip: '清空搜索',
                      onPressed: () => setState(_search.clear),
                      icon: const Icon(Icons.close_rounded),
                    ),
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 4),
          child: Text(
            '${collections.length} 部 · ${collections.fold<int>(0, (sum, c) => sum + c.jobs.length)} 集'
            '${_filter == 'archived' ? ' · 已清理任务，视频仍保留' : ''}',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        if (_updater.busy || _busy) LinearProgressIndicator(minHeight: 2),
        if (_updater.busy || _busy)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    _busy ? _operation : _updater.status,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (_updater.busy)
                  TextButton(
                    onPressed: _updater.cancel,
                    child: const Text('停止'),
                  ),
              ],
            ),
          ),
        if (_error != null && _jobs.isNotEmpty)
          Padding(padding: const EdgeInsets.all(12), child: Text(_error!)),
        Expanded(
          child: _loading
              ? const Center(child: CircularProgressIndicator())
              : _error != null && _jobs.isEmpty
              ? StatusPanel(
                  title: '无法读取下载记录',
                  message: _error!,
                  onRetry: _refresh,
                )
              : rows.isEmpty
              ? StatusPanel(
                  title: _jobs.isEmpty ? '还没有下载任务' : '暂无符合条件的任务',
                  message: '在剧集详情选择下载，或调整筛选查看已保留的视频。',
                  icon: Icons.download_outlined,
                )
              : ListView.builder(
                  padding: const EdgeInsets.fromLTRB(12, 4, 12, 20),
                  itemCount: rows.length,
                  itemBuilder: (_, index) => switch (rows[index]) {
                    DownloadCollection collection => _collection(collection),
                    DownloadJob job => _episode(job),
                    _ => const SizedBox.shrink(),
                  },
                ),
        ),
        if (_selecting) _batchBar(),
      ],
    );
    if (widget.embedded) return body;
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.escape): () =>
            Navigator.of(context).maybePop(),
        const SingleActivator(LogicalKeyboardKey.goBack): () =>
            Navigator.of(context).maybePop(),
      },
      child: Scaffold(
        appBar: AppBar(
          title: Text(title, maxLines: 1, overflow: TextOverflow.ellipsis),
          actions: compact ? null : actions,
        ),
        body: SafeArea(top: false, child: body),
      ),
    );
  }
}
