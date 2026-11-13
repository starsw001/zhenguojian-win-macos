import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'local_media_actions.dart';
import 'local_media_catalog.dart';
import 'models.dart';
import 'settings_screen.dart';

class LocalMediaExportSnapshot {
  const LocalMediaExportSnapshot(this.catalog, this.groups, {this.warning});

  final LocalMediaCatalog catalog;
  final List<LocalExportGroup> groups;
  final String? warning;
  List<LocalExportGroup> get pending =>
      groups.where((group) => group.pending.isNotEmpty).toList();
}

class LocalMediaExportChoice {
  const LocalMediaExportChoice({this.directory, this.jobs = const []});

  final String? directory;
  final List<DownloadJob> jobs;
}

class LocalMediaExportDialog extends StatefulWidget {
  const LocalMediaExportDialog({
    super.key,
    required this.snapshot,
    required this.refresh,
  });

  final LocalMediaExportSnapshot snapshot;
  final Future<LocalMediaExportSnapshot> Function() refresh;

  @override
  State<LocalMediaExportDialog> createState() => _LocalMediaExportDialogState();
}

class _LocalMediaExportDialogState extends State<LocalMediaExportDialog> {
  late LocalMediaExportSnapshot _snapshot = widget.snapshot;
  bool _pending = false, _refreshing = false;
  String? _error;

  Future<void> _refresh() async {
    if (_refreshing) return;
    setState(() => _refreshing = true);
    try {
      final snapshot = await widget.refresh();
      if (!mounted) return;
      setState(() {
        _snapshot = snapshot;
        _error = null;
      });
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _refreshing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final folders = _snapshot.catalog.folders;
    final groups = _snapshot.pending;
    final pending = groups.expand((group) => group.pending).toList();
    final count = _pending ? groups.length : folders.length;
    final enabled = !_refreshing && _error == null;
    final videos = folders.fold(0, (sum, folder) => sum + folder.files.length);
    return AlertDialog(
      title: Row(
        children: [
          Expanded(child: Text(_pending ? '导出尚未保存的下载分集' : '保存哪部剧的独立 MP4？')),
          IconButton(
            key: const ValueKey('local-media-picker-refresh'),
            tooltip: '刷新下载目录',
            onPressed: _refreshing ? null : _refresh,
            icon: _refreshing
                ? const SizedBox.square(
                    dimension: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.refresh),
          ),
        ],
      ),
      content: SizedBox(
        width: 520,
        height: ((count + 1) * 84.0).clamp(
          84,
          math.max(84, MediaQuery.sizeOf(context).height * .5),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (_error != null) Text(_error!),
            if (_snapshot.warning != null) Text(_snapshot.warning!),
            Expanded(
              child: count == 0
                  ? Center(
                      child: Text(
                        _pending ? '没有待导出的已下载分集' : '下载目录的 exports 下暂无视频目录',
                      ),
                    )
                  : ListView.builder(
                      itemCount: count + 1,
                      itemExtent: 84,
                      itemBuilder: (context, index) {
                        if (index == 0) {
                          return ListTile(
                            key: ValueKey(
                              _pending
                                  ? 'export-all-pending'
                                  : 'export-all-folders',
                            ),
                            title: Text(
                              _pending
                                  ? '全部待导出 · ${pending.length} 集'
                                  : '全部目录 · ${folders.length} 个目录 · $videos 个视频',
                            ),
                            subtitle: Text(
                              _pending
                                  ? '仅处理尚未保存的下载任务，不重复导出已有视频'
                                  : '与独立 MP4 列表一致，按当前下载目录读取',
                            ),
                            enabled: enabled,
                            onTap: () => Navigator.pop(
                              context,
                              LocalMediaExportChoice(
                                jobs: _pending ? pending : const [],
                              ),
                            ),
                          );
                        }
                        if (_pending) {
                          final group = groups[index - 1];
                          return ListTile(
                            key: ValueKey('export-group:${group.drama.id}'),
                            title: Text(
                              group.drama.title,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                            subtitle: Text('待导出 ${group.pending.length} 集'),
                            trailing: const Icon(Icons.output),
                            enabled: enabled,
                            onTap: () => Navigator.pop(
                              context,
                              LocalMediaExportChoice(jobs: group.pending),
                            ),
                          );
                        }
                        final folder = folders[index - 1];
                        return ListTile(
                          key: ValueKey('export-folder:${folder.directory}'),
                          title: Text(
                            folder.name,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          subtitle: Text(
                            folder.error.isEmpty
                                ? '${folder.files.length} 个视频 · ${storageSize(folder.bytes)}'
                                : folder.error,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                          ),
                          trailing: const Icon(Icons.folder_open),
                          enabled: enabled,
                          onTap: () => Navigator.pop(
                            context,
                            LocalMediaExportChoice(directory: folder.directory),
                          ),
                        );
                      },
                    ),
            ),
          ],
        ),
      ),
      actions: [
        if (_pending || pending.isNotEmpty)
          TextButton(
            key: const ValueKey('local-media-picker-pending'),
            onPressed: enabled
                ? () => setState(() => _pending = !_pending)
                : null,
            child: Text(_pending ? '返回下载目录' : '待导出任务（${pending.length} 集）'),
          ),
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ],
    );
  }
}
