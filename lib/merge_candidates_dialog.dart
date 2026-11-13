import 'package:flutter/material.dart';

import 'local_merge_candidates.dart';

class MergeCandidateSelection {
  const MergeCandidateSelection(this.candidates, {this.cleanup = false});

  final List<LocalMergeCandidate> candidates;
  final bool cleanup;
}

class MergeCandidatesDialog extends StatefulWidget {
  const MergeCandidatesDialog({
    super.key,
    required this.snapshot,
    required this.refresh,
    this.initialDramaId,
  });

  final LocalMergeSnapshot snapshot;
  final Future<LocalMergeSnapshot> Function() refresh;
  final String? initialDramaId;

  @override
  State<MergeCandidatesDialog> createState() => _MergeCandidatesDialogState();
}

class _MergeCandidatesDialogState extends State<MergeCandidatesDialog> {
  late LocalMergeSnapshot _snapshot = widget.snapshot;
  late final Set<String> _selected = {
    for (final row in _snapshot.candidates)
      if (row.id == widget.initialDramaId && row.available) row.id,
  };
  bool _cleanup = false, _refreshing = false;
  String? _error, _notice;

  Future<void> _refresh() async {
    if (_refreshing) return;
    setState(() => _refreshing = true);
    try {
      final snapshot = await widget.refresh();
      if (!mounted) return;
      final before = {
        for (final row in _snapshot.candidates) row.id: row.fingerprint,
      };
      final after = {
        for (final row in snapshot.candidates)
          if (row.available) row.id: row.fingerprint,
      };
      final selectedCount = _selected.length;
      _selected.removeWhere((id) => before[id] != after[id]);
      setState(() {
        _snapshot = snapshot;
        _error = null;
        _notice = _selected.length != selectedCount
            ? '所选剧集的本地文件已变化，请重新选择；没有删除任何记录或视频。'
            : null;
      });
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _refreshing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final ready = !_refreshing && _error == null;
    return AlertDialog(
      title: Row(
        children: [
          const Expanded(child: Text('选择要合并的剧')),
          IconButton(
            key: const ValueKey('merge-picker-refresh'),
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
        height: MediaQuery.sizeOf(context).height * .6,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text('按当前下载目录中实际存在的连续分集合并，每次最多加入 50 部。'),
            if (_snapshot.missing > 0)
              Text('已忽略 ${_snapshot.missing} 条文件缺失或不匹配的旧记录，未删除记录或恢复文件。'),
            if (_snapshot.unregistered > 0)
              Text('另有 ${_snapshot.unregistered} 个未登记视频，无法确认分集归属，未猜测或加入队列。'),
            if (_notice != null) Text(_notice!),
            if (_error != null) Text(_error!),
            const SizedBox(height: 8),
            Expanded(
              child: _snapshot.candidates.isEmpty
                  ? const Center(child: Text('当前下载目录没有可合并的已登记分集'))
                  : ListView.builder(
                      itemCount: _snapshot.candidates.length,
                      itemBuilder: (_, index) {
                        final row = _snapshot.candidates[index];
                        return CheckboxListTile(
                          key: ValueKey('merge-candidate:${row.id}'),
                          contentPadding: EdgeInsets.zero,
                          value: _selected.contains(row.id),
                          title: Text(row.drama.title),
                          subtitle: Text(row.error ?? row.label),
                          onChanged: !ready || !row.available
                              ? null
                              : (value) => setState(() {
                                  if (value == true && _selected.length < 50) {
                                    _selected.add(row.id);
                                  } else {
                                    _selected.remove(row.id);
                                  }
                                }),
                        );
                      },
                    ),
            ),
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              value: _cleanup,
              title: const Text('完成后删除原分集'),
              subtitle: const Text('仅在成品通过完整解码并保存后删除；默认保留原视频。'),
              onChanged: ready
                  ? (value) => setState(() => _cleanup = value == true)
                  : null,
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          key: const ValueKey('merge-picker-accept'),
          onPressed: !ready || _selected.isEmpty
              ? null
              : () => Navigator.pop(
                  context,
                  MergeCandidateSelection([
                    for (final row in _snapshot.candidates)
                      if (_selected.contains(row.id)) row,
                  ], cleanup: _cleanup),
                ),
          child: Text('加入 ${_selected.length} 部'),
        ),
      ],
    );
  }
}
