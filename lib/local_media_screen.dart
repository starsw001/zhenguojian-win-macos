import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show ScrollCacheExtent;
import 'package:flutter/services.dart';
import 'package:path/path.dart' as path;

import 'core_bridge.dart';
import 'download_folders.dart';
import 'local_media_catalog.dart';
import 'local_media_actions.dart';
import 'local_media_export_dialog.dart';
import 'local_store.dart';
import 'media_library.dart';
import 'models.dart';
import 'player_screen.dart';
import 'settings_screen.dart';
import 'merge_queue_screen.dart';

class LocalMediaScreen extends StatefulWidget {
  const LocalMediaScreen({
    super.key,
    required this.repository,
    required this.store,
  });
  final AppRepository repository;
  final LocalStore store;
  @override
  State<LocalMediaScreen> createState() => _LocalMediaScreenState();
}

class _LocalMediaScreenState extends State<LocalMediaScreen> {
  late final MediaLibrary library =
      MediaLibrary.current ?? MediaLibrary(widget.repository, widget.store);
  List<DownloadJob> _jobs = [];
  bool _loading = true, _merged = true;
  String? _error, _directory, _notice, _exportedDrama, _downloadsWarning;
  List<LocalExportGroup> _exportGroups = const [];
  Map<String, LocalExportGroup> _groupsByDirectory = const {};
  String? _focusedOutput;
  bool _choosing = false;
  Future<void>? _refreshPending;
  LocalMediaCatalog _catalog = const LocalMediaCatalog();
  Map<String, LocalMediaFolder> _folders = const {};
  final _folderScroll = ScrollController();
  final _fileScroll = ScrollController(keepScrollOffset: false);
  final _mergedScroll = ScrollController();
  bool _refreshing = false, _refreshAgain = false, _wasBusy = false;
  int _refreshRevision = 0;
  late String _permissions;

  @override
  void initState() {
    super.initState();
    _permissions = _accessStamp();
    _wasBusy = library.busy;
    library.addListener(_libraryChanged);
    widget.store.addListener(_storeChanged);
    _refresh();
  }

  @override
  void dispose() {
    _refreshRevision++;
    library.removeListener(_libraryChanged);
    widget.store.removeListener(_storeChanged);
    _folderScroll.dispose();
    _fileScroll.dispose();
    _mergedScroll.dispose();
    if (library != MediaLibrary.current && !library.busy) library.dispose();
    super.dispose();
  }

  String _accessStamp() =>
      '${widget.store.profileEpoch}:${widget.store.canDownload}:'
      '${widget.store.sources.map((source) => source.id).join(',')}';

  void _storeChanged() {
    final permissions = _accessStamp();
    if (permissions == _permissions || !mounted) return;
    _permissions = permissions;
    _refreshRevision++;
    setState(() {
      _catalog = const LocalMediaCatalog();
      _folders = const {};
      _directory = null;
      _jobs = [];
      _exportGroups = const [];
      _groupsByDirectory = const {};
      _notice = null;
      _downloadsWarning = null;
      _exportedDrama = null;
      _focusedOutput = null;
    });
    if (_refreshing) {
      _refreshAgain = true;
    } else {
      unawaited(_refresh());
    }
  }

  void _libraryChanged() {
    if (library.busy) {
      _wasBusy = true;
    } else if (_wasBusy && mounted) {
      _wasBusy = false;
      if (_refreshing) {
        _refreshAgain = true;
      } else {
        unawaited(_refresh());
      }
    }
  }

  Future<void> _refresh() {
    final pending = _refreshPending;
    if (pending != null) return pending;
    late final Future<void> result;
    result =
        (() async {
          do {
            _refreshAgain = false;
            await _readCatalog();
          } while (mounted && _refreshAgain);
        })().whenComplete(() {
          if (identical(_refreshPending, result)) _refreshPending = null;
        });
    _refreshPending = result;
    return result;
  }

  Future<void> _readCatalog() async {
    if (_refreshing || !mounted) return;
    final revision = ++_refreshRevision;
    final permissions = _accessStamp();
    setState(() => _refreshing = true);
    try {
      if (!widget.store.canDownload) {
        throw AppFailure('当前用户没有本地媒体权限');
      }
      await library.reload();
      final catalog = await scanLocalMediaCatalog(
        library.root!,
        library.items,
        widget.store.sources.map((source) => source.id).toSet(),
      );
      var jobs = <DownloadJob>[];
      String? downloadsWarning;
      try {
        jobs = await widget.repository.downloads();
      } catch (error) {
        downloadsWarning = '下载任务读取失败，目录视频仍可查看：$error';
      }
      if (mounted &&
          revision == _refreshRevision &&
          permissions == _accessStamp()) {
        setState(() {
          _jobs = jobs.where((job) => job.completed).toList();
          _downloadsWarning = downloadsWarning;
          _catalog = catalog;
          _exportGroups = localExportGroups(_jobs, catalog);
          _groupsByDirectory = {
            for (final group in _exportGroups)
              if (group.directory != null) group.directory!: group,
          };
          _folders = {
            for (final folder in catalog.folders) folder.directory: folder,
          };
          if (!_folders.containsKey(_directory)) _directory = null;
          _error = null;
        });
      }
    } catch (error) {
      if (mounted && revision == _refreshRevision) {
        setState(() => _error = error.toString());
      }
    } finally {
      if (mounted) {
        setState(() {
          _loading = false;
          _refreshing = false;
        });
      }
    }
  }

  void _enterDirectory(String? directory) {
    if (_fileScroll.hasClients) _fileScroll.jumpTo(0);
    setState(() => _directory = directory);
  }

  Future<void> _showInExplorer(String directory) async {
    try {
      await const DownloadFolderOpener().open(directory);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _create(bool merge, {String? initialDramaId}) async {
    if (_choosing || (!merge && library.busy)) return;
    final epoch = widget.store.profileEpoch;
    final permissions = _accessStamp();
    setState(() => _choosing = true);
    try {
      if (merge) {
        final output = await Navigator.push<String>(
          context,
          MaterialPageRoute<String>(
            builder: (_) => MergeQueueScreen(
              library: library,
              repository: widget.repository,
              store: widget.store,
              addOnOpen: true,
              initialDramaId: initialDramaId,
            ),
          ),
        );
        if (!mounted ||
            epoch != widget.store.profileEpoch ||
            permissions != _accessStamp()) {
          return;
        }
        await _refresh();
        if (!mounted ||
            epoch != widget.store.profileEpoch ||
            permissions != _accessStamp()) {
          return;
        }
        setState(() {
          _merged = true;
          _focusedOutput = output;
        });
        if (output != null) {
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (!mounted || !_merged || !_mergedScroll.hasClients) return;
            final index = _catalog.merged.indexWhere(
              (item) => item.id == output,
            );
            if (index < 0) return;
            final scale = MediaQuery.textScalerOf(context).scale(16) / 16;
            _mergedScroll.jumpTo(
              (index * (158 + 56 * scale)).clamp(
                0,
                _mergedScroll.position.maxScrollExtent,
              ),
            );
          });
        }
        return;
      }
      await _refresh();
      if (!mounted ||
          epoch != widget.store.profileEpoch ||
          permissions != _accessStamp()) {
        return;
      }
      if (_error != null) throw AppFailure(_error!);
      LocalMediaExportSnapshot snapshot() => LocalMediaExportSnapshot(
        _catalog,
        List.unmodifiable(_exportGroups),
        warning: _downloadsWarning,
      );
      final selected = await showDialog<LocalMediaExportChoice>(
        context: context,
        builder: (context) => LocalMediaExportDialog(
          snapshot: snapshot(),
          refresh: () async {
            await _refresh();
            if (!mounted ||
                epoch != widget.store.profileEpoch ||
                permissions != _accessStamp()) {
              throw AppFailure('用户权限已变化，请关闭后重新打开');
            }
            if (_error != null) throw AppFailure(_error!);
            return snapshot();
          },
        ),
      );
      if (selected == null ||
          !mounted ||
          epoch != widget.store.profileEpoch ||
          permissions != _accessStamp()) {
        return;
      }
      await _refresh();
      if (!mounted ||
          epoch != widget.store.profileEpoch ||
          permissions != _accessStamp()) {
        return;
      }
      if (_error != null) throw AppFailure(_error!);
      if (selected.jobs.isEmpty) {
        final directory = selected.directory;
        if (directory != null && !_folders.containsKey(directory)) {
          throw AppFailure('所选目录已移动或删除，列表已刷新，请重新选择');
        }
        _enterDirectory(directory);
        setState(() {
          _merged = false;
          final group = _groupsByDirectory[directory];
          _exportedDrama = group != null && group.jobs.length >= 2
              ? group.drama.id
              : null;
          _notice = '已按下载目录显示现有视频，无需重复导出；原文件和记录未改动。';
        });
        return;
      }
      final selectedIds = selected.jobs.map((job) => job.id).toSet();
      final pending = _exportGroups
          .expand((group) => group.pending)
          .where((job) => selectedIds.contains(job.id))
          .toList();
      final dramaIds = pending.map((job) => job.drama.id).toSet();
      final selectedDrama = dramaIds.length == 1 ? dramaIds.single : null;
      final count = pending.length;
      setState(() => _error = null);
      if (pending.isNotEmpty) {
        await library.exportJobs(pending);
        await _refresh();
      }
      if (!mounted ||
          epoch != widget.store.profileEpoch ||
          permissions != _accessStamp()) {
        return;
      }
      final current = selectedDrama == null
          ? null
          : _exportGroups
                .where((group) => group.drama.id == selectedDrama)
                .firstOrNull;
      final title = current == null
          ? '$count 集'
          : '《${current.drama.title}》$count 集';
      setState(() {
        _merged = false;
        _directory = current?.directory;
        _exportedDrama = current != null && current.jobs.length >= 2
            ? current.drama.id
            : null;
        _notice =
            '$title 独立 MP4 已保存${pending.isEmpty ? '，无需重复导出' : ''}。导出分集不会自动合并；需要成品请另行合并。';
      });
    } catch (error) {
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _error = error.toString());
        await _refresh();
      }
    } finally {
      if (mounted) setState(() => _choosing = false);
    }
  }

  Future<void> _play(LocalMediaItem item) async {
    final watch = widget.store.mediaWatched(item.id);
    final episode = Episode({
      'id': item.id,
      'title': item.merged ? '合并视频' : '第 ${item.episodes.first} 集',
      'currentEpisode': item.episodes.first,
    }, 1);
    await Navigator.push(
      context,
      MaterialPageRoute<void>(
        builder: (_) => PlayerScreen(
          detail: DramaDetail(item.drama, [episode]),
          initialIndex: 0,
          initialPosition: watch == null || watch.finished ? 0 : watch.position,
          repository: _LocalFileRepository(
            widget.repository,
            widget.store,
            item,
            library.fileFor(item),
          ),
          store: widget.store,
          localOnly: true,
          allowOnlineFallback: false,
          mediaId: item.id,
        ),
      ),
    );
    if (mounted) await _refresh();
  }

  Future<void> _remove(LocalMediaItem item) async {
    final yes = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('删除此媒体文件？'),
        content: Text(
          '${item.merged ? '将删除此合并成品及其专用目录。' : '将删除此成品文件；删完后对应剧集目录为空时会一并删除，非空目录保留。'}已单独保存的原分集不受影响。${item.merged || item.special ? '' : '此分集不再自动导出，需要时可手动重新导出。'}',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('保留'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (yes != true || !mounted) return;
    try {
      await library.remove(item);
      if (mounted) await _refresh();
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _exportMerged(LocalMediaItem item) async {
    try {
      final exported = await library.exportMerged(item);
      if (mounted) {
        setState(() {
          _merged = false;
          _directory = path.joinAll(path.split(exported.file).take(2));
          _error = null;
        });
        await _refresh();
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Widget _folderCard(LocalMediaFolder folder) => Card(
    key: ValueKey('local-media-folder:${folder.directory}'),
    child: ListTile(
      leading: const Icon(Icons.folder_outlined),
      title: Text(folder.name, maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: Text(
        folder.error.isEmpty
            ? '${folder.files.length} 个视频 · ${storageSize(folder.bytes)}'
            : folder.error,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: const Icon(Icons.chevron_right),
      onTap: () => _enterDirectory(folder.directory),
    ),
  );

  Widget _fileCard(LocalMediaFile file) {
    final item = file.item;
    final full = path.join(library.root!, file.file);
    return Card(
      key: ValueKey('local-media-file:${file.file}'),
      child: ListTile(
        leading: IconButton(
          tooltip: item == null ? '未登记文件，可在文件夹中管理' : '本地播放',
          onPressed: item == null ? null : () => _play(item),
          icon: Icon(
            item == null ? Icons.movie_outlined : Icons.play_circle_outline,
          ),
        ),
        title: Text(file.title, maxLines: 1, overflow: TextOverflow.ellipsis),
        subtitle: Text(
          '${path.relative(file.file, from: _directory!)} · ${storageSize(file.bytes)}'
          '${item == null ? '\n未登记文件，可在文件夹中管理' : ''}',
          maxLines: 3,
          overflow: TextOverflow.ellipsis,
        ),
        onTap: item == null ? null : () => _play(item),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              tooltip: '复制文件路径',
              onPressed: () => Clipboard.setData(ClipboardData(text: full)),
              icon: const Icon(Icons.copy),
            ),
            if (item != null)
              IconButton(
                tooltip: '删除成品',
                onPressed: library.busy ? null : () => _remove(item),
                icon: const Icon(Icons.delete_outline),
              )
            else if (const DownloadFolderOpener().supported)
              IconButton(
                tooltip: '打开所在文件夹',
                onPressed: () => _showInExplorer(path.dirname(full)),
                icon: const Icon(Icons.folder_open),
              ),
          ],
        ),
      ),
    );
  }

  Widget _mergedCard(LocalMediaItem item) => Card(
    key: ValueKey('local-media-merged:${item.id}'),
    color: item.id == _focusedOutput
        ? Theme.of(context).colorScheme.secondaryContainer
        : null,
    child: Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            item.drama.title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.titleMedium,
          ),
          const SizedBox(height: 6),
          Text(
            '已合并 ${item.episodes.length} 集 · ${storageSize(item.bytes)} · ${path.extension(item.file).substring(1).toUpperCase()}',
          ),
          Text(
            '视频转码 ${item.videoTranscodes} 集 · 音轨处理 ${item.audioTranscodes} 集',
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodySmall,
          ),
          TextButton.icon(
            onPressed: library.busy ? null : () => _exportMerged(item),
            icon: const Icon(Icons.output_rounded),
            label: const Text('另存特别篇视频'),
          ),
          Row(
            children: [
              TextButton.icon(
                onPressed: () => _play(item),
                icon: const Icon(Icons.play_arrow),
                label: Text(
                  item.episodes.length == item.drama.episodes ? '全集播放' : '本地播放',
                ),
              ),
              const Spacer(),
              if (const DownloadFolderOpener().supported)
                IconButton(
                  tooltip: '打开合并成品文件夹',
                  onPressed: () =>
                      _showInExplorer(path.dirname(library.fileFor(item))),
                  icon: const Icon(Icons.folder_open),
                ),
              IconButton(
                tooltip: '复制文件路径',
                onPressed: () => Clipboard.setData(
                  ClipboardData(text: library.fileFor(item)),
                ),
                icon: const Icon(Icons.copy),
              ),
              IconButton(
                tooltip: '删除成品',
                onPressed: library.busy ? null : () => _remove(item),
                icon: const Icon(Icons.delete_outline),
              ),
            ],
          ),
        ],
      ),
    ),
  );

  Widget _mediaList(BuildContext context) {
    if (!widget.store.canDownload) {
      return const Center(child: Text('当前用户没有本地媒体权限'));
    }
    if (_loading) return const Center(child: CircularProgressIndicator());
    final folder = _folders[_directory];
    final length = _merged
        ? _catalog.merged.length
        : folder?.files.length ?? _catalog.folders.length;
    if (length == 0) {
      if (_merged) {
        return Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text('尚无合并成品。独立 MP4 导出不会自动合并。'),
              const SizedBox(height: 12),
              FilledButton.icon(
                key: const ValueKey('local-media-start-merge'),
                onPressed: _choosing ? null : () => _create(true),
                icon: const Icon(Icons.merge),
                label: const Text('合并已保存的分集'),
              ),
            ],
          ),
        );
      }
      return Center(
        child: Text(
          _merged
              ? '合并成品会显示在这里，可直接播放和续播。'
              : folder == null
              ? 'exports 下暂无视频目录，保存分集后点击刷新。'
              : '此目录暂无可用视频，点击刷新重新读取。',
        ),
      );
    }
    final scale = MediaQuery.textScalerOf(context).scale(16) / 16;
    final controller = _merged
        ? _mergedScroll
        : folder == null
        ? _folderScroll
        : _fileScroll;
    return Scrollbar(
      controller: controller,
      thumbVisibility:
          Platform.isWindows || Platform.isMacOS || Platform.isLinux,
      child: ListView.builder(
        key: PageStorageKey(
          'local-media-list:${_merged ? 'merged' : _directory ?? 'exports'}',
        ),
        controller: controller,
        padding: const EdgeInsets.fromLTRB(12, 0, 12, 16),
        itemExtent: _merged
            ? 158 + 56 * scale
            : folder == null
            ? 70 + 30 * scale
            : 100 + 25 * scale,
        scrollCacheExtent: const ScrollCacheExtent.pixels(240),
        addAutomaticKeepAlives: false,
        itemCount: length,
        itemBuilder: (context, index) => _merged
            ? _mergedCard(_catalog.merged[index])
            : folder == null
            ? _folderCard(_catalog.folders[index])
            : _fileCard(folder.files[index]),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: library,
    builder: (_, _) => PopScope<void>(
      canPop: _merged || _directory == null,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop && !_merged && _directory != null) _enterDirectory(null);
      },
      child: Scaffold(
        appBar: AppBar(
          title: const Text('本地媒体'),
          leading: BackButton(
            onPressed: () {
              if (!_merged && _directory != null) {
                _enterDirectory(null);
              } else {
                Navigator.maybePop(context);
              }
            },
          ),
          actions: [
            IconButton(
              tooltip: '刷新',
              onPressed: library.busy || _refreshing ? null : _refresh,
              icon: _refreshing
                  ? const SizedBox.square(
                      dimension: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh),
            ),
          ],
        ),
        body: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Wrap(
                spacing: 12,
                runSpacing: 8,
                children: [
                  ChoiceChip(
                    label: const Text('合并成品'),
                    selected: _merged,
                    onSelected: (_) => setState(() => _merged = true),
                  ),
                  ChoiceChip(
                    label: const Text('独立 MP4'),
                    selected: !_merged,
                    onSelected: (_) => setState(() => _merged = false),
                  ),
                  FilledButton.icon(
                    onPressed:
                        _choosing || _refreshing || library.busy && !_merged
                        ? null
                        : () => _create(_merged),
                    icon: Icon(_merged ? Icons.merge : Icons.output),
                    label: Text(_merged ? '合并已下载分集' : '导出已下载分集'),
                  ),
                ],
              ),
            ),
            if (library.busy)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 20),
                child: Column(
                  children: [
                    LinearProgressIndicator(
                      value: library.progress > 0 ? library.progress : null,
                    ),
                    const SizedBox(height: 8),
                    Row(
                      children: [
                        Expanded(child: Text(library.status)),
                        TextButton(
                          onPressed: () => unawaited(library.cancel()),
                          child: const Text('取消'),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            if (_error != null || library.error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.all(16),
                child: Text(_error ?? library.error),
              ),
            if (!_merged && _notice != null)
              Padding(
                key: const ValueKey('local-media-export-result'),
                padding: const EdgeInsets.symmetric(
                  horizontal: 20,
                  vertical: 8,
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(_notice!),
                    if (_exportedDrama != null)
                      TextButton.icon(
                        key: const ValueKey('local-media-merge-exported'),
                        onPressed: _choosing || library.busy
                            ? null
                            : () =>
                                  _create(true, initialDramaId: _exportedDrama),
                        icon: const Icon(Icons.merge),
                        label: const Text('合并这部剧'),
                      ),
                  ],
                ),
              ),
            if (!_merged)
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 20,
                  vertical: 8,
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      '按 exports 实际子目录查看视频，点击目录进入剧集；刷新只读更新列表，不移动文件或重下视频。',
                    ),
                    Row(
                      children: [
                        TextButton.icon(
                          key: const ValueKey('local-media-exports-root'),
                          onPressed: _directory == null
                              ? null
                              : () => _enterDirectory(null),
                          icon: Icon(
                            _directory == null
                                ? Icons.folder_outlined
                                : Icons.arrow_back,
                          ),
                          label: const Text('exports'),
                        ),
                        Expanded(
                          child: Text(
                            _directory == null
                                ? '${_catalog.folders.length} 个目录'
                                : _folders[_directory]?.name ?? '',
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                        if (library.root != null)
                          IconButton(
                            tooltip: '复制 MP4 媒体目录',
                            onPressed: () => Clipboard.setData(
                              ClipboardData(
                                text: path.join(
                                  library.root!,
                                  _directory ?? 'exports',
                                ),
                              ),
                            ),
                            icon: const Icon(Icons.copy),
                          ),
                      ],
                    ),
                    if (_groupsByDirectory[_directory]
                        case final LocalExportGroup group
                        when group.jobs.length >= 2)
                      TextButton.icon(
                        key: const ValueKey('local-media-merge-folder'),
                        onPressed: _choosing || library.busy
                            ? null
                            : () =>
                                  _create(true, initialDramaId: group.drama.id),
                        icon: const Icon(Icons.merge),
                        label: Text('合并本剧已下载的 ${group.jobs.length} 集'),
                      ),
                    if (_folders[_directory]?.error.isNotEmpty == true)
                      Text(_folders[_directory]!.error),
                  ],
                ),
              ),
            Expanded(child: _mediaList(context)),
          ],
        ),
      ),
    ),
  );
}

class _LocalFileRepository extends AppRepository {
  _LocalFileRepository(this.parent, this.store, this.item, this.file);
  final AppRepository parent;
  final LocalStore store;
  final LocalMediaItem item;
  final String file;
  Future<PlaybackPlan> _plan() async {
    if (!store.canDownload || !store.allowsSource(item.drama.source)) {
      throw AppFailure('当前用户无权读取此视频');
    }
    if (!await File(file).exists() || await File(file).length() == 0) {
      throw AppFailure('本地成品文件缺失，请重新生成。', code: 'local_media');
    }
    return PlaybackPlan(url: file, local: true);
  }

  @override
  Future<void> initialize() async {}
  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) => parent.catalog(
    source,
    page: page,
    query: query,
    category: category,
    force: force,
  );
  @override
  Future<CatalogPage> cached(String source, {String category = ''}) =>
      parent.cached(source, category: category);
  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) => parent.categories(source, force: force);
  @override
  Future<String> cover(Drama drama, {bool force = false}) =>
      parent.cover(drama, force: force);
  @override
  Future<DramaDetail> detail(Drama drama) => parent.detail(drama);
  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) => _plan();
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) => _plan();
  @override
  Future<PlaybackPlan> resolveOnline(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) => throw AppFailure('此成品仅支持本地播放');
  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) => _plan();
  @override
  Future<void> cancelPlayback() async {}
  @override
  Future<void> release(String session) async {}
}
