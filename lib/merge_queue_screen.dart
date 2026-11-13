import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import 'core_bridge.dart';
import 'local_store.dart';
import 'local_merge_candidates.dart';
import 'merge_candidates_dialog.dart';
import 'media_library.dart';
import 'widgets.dart';

class MergeQueueScreen extends StatefulWidget {
  const MergeQueueScreen({
    super.key,
    required this.library,
    required this.repository,
    required this.store,
    this.addOnOpen = false,
    this.initialDramaId,
  });
  final MediaLibrary library;
  final AppRepository repository;
  final LocalStore store;
  final bool addOnOpen;
  final String? initialDramaId;
  @override
  State<MergeQueueScreen> createState() => _MergeQueueScreenState();
}

class _MergeQueueScreenState extends State<MergeQueueScreen> {
  bool _loading = true, _adding = false, _refreshing = false;
  LocalMergeSnapshot _snapshot = const LocalMergeSnapshot();
  Future<LocalMergeSnapshot>? _candidateRead;
  String? _error;
  late final int _epoch = widget.store.profileEpoch;
  bool get _allowed =>
      widget.store.canDownload && widget.store.profileEpoch == _epoch;
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      await widget.library.merges.load();
      if (mounted && _allowed) {
        if (widget.addOnOpen) {
          await _add();
        } else {
          await _readCandidates();
        }
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  String _sourceStamp() =>
      widget.store.sources.map((source) => source.id).join(',');

  Future<LocalMergeSnapshot> _readCandidates() {
    final pending = _candidateRead;
    if (pending != null) return pending;
    if (!mounted || !_allowed) throw AppFailure('用户已切换，请重新打开合并队列');
    final stamp = _sourceStamp();
    setState(() => _refreshing = true);
    late final Future<LocalMergeSnapshot> result;
    result =
        (() async {
          try {
            final snapshot = await readLocalMergeSnapshot(
              widget.library,
              widget.repository,
              widget.store.sources.map((source) => source.id).toSet(),
            );
            if (!mounted || !_allowed || stamp != _sourceStamp()) {
              throw AppFailure('用户权限已变化，请重新打开合并队列');
            }
            setState(() {
              _snapshot = snapshot;
              _error = null;
            });
            return snapshot;
          } finally {
            if (mounted) setState(() => _refreshing = false);
          }
        })().whenComplete(() {
          if (identical(_candidateRead, result)) _candidateRead = null;
        });
    _candidateRead = result;
    return result;
  }

  Future<void> _refresh() async {
    try {
      await _readCandidates();
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _add() async {
    if (_adding || !_allowed) return;
    setState(() => _adding = true);
    try {
      final snapshot = await _readCandidates();
      if (!mounted || !_allowed) return;
      final selected = await showDialog<MergeCandidateSelection>(
        context: context,
        builder: (_) => MergeCandidatesDialog(
          snapshot: snapshot,
          refresh: _readCandidates,
          initialDramaId: widget.initialDramaId,
        ),
      );
      if (selected == null || !mounted || !_allowed) return;
      final current = await _readCandidates();
      if (!mounted || !_allowed) return;
      final available = {
        for (final row in current.candidates)
          if (row.available) row.id: row,
      };
      for (final row in selected.candidates) {
        if (available[row.id]?.fingerprint != row.fingerprint) {
          throw AppFailure('本地分集已移动、删除或改变，请刷新后重新选择；原有记录和文件保留。');
        }
      }
      final count = await widget.library.merges.enqueue([
        for (final row in selected.candidates) available[row.id]!.jobs,
      ], cleanup: selected.cleanup);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              count == 0
                  ? '已有相同任务或合并成品，不会重复合并。完成的任务可点击「查看合并成品」。'
                  : '已加入 $count 部；完成后点击「查看合并成品」，默认保留原分集。',
            ),
          ),
        );
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _adding = false);
    }
  }

  Future<void> _control(MergeQueueJob job, String action) async {
    if (!_allowed) return;
    try {
      await widget.library.merges.control(job.id, action);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _openOutput(MergeQueueJob job) async {
    if (!_allowed) return;
    try {
      await widget.library.reload();
      if (!mounted || !_allowed) return;
      final item = widget.library.items
          .where((item) => item.id == job.outputId && item.merged)
          .firstOrNull;
      if (item == null) throw AppFailure('合并成品记录缺失，请刷新后重试');
      final file = File(widget.library.fileFor(item));
      if (!await file.exists() || await file.length() != item.bytes) {
        throw AppFailure('合并成品已移动、删除或改变，原分集和记录保留');
      }
      if (mounted && _allowed) Navigator.pop(context, job.outputId);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.library,
    builder: (context, _) {
      final jobs = widget.library.merges.items;
      return Scaffold(
        appBar: AppBar(
          title: const Text('合并队列'),
          actions: [
            IconButton(
              key: const ValueKey('merge-queue-refresh'),
              tooltip: '刷新下载目录',
              onPressed: _adding || _refreshing || !_allowed ? null : _refresh,
              icon: _refreshing
                  ? const SizedBox.square(
                      dimension: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh),
            ),
            IconButton(
              tooltip: '添加多部剧',
              onPressed: _adding || !_allowed ? null : _add,
              icon: const Icon(Icons.add_rounded),
            ),
          ],
        ),
        body: !_allowed
            ? const StatusPanel(title: '当前用户已切换', message: '请返回后重新打开合并队列。')
            : _loading
            ? const Center(child: CircularProgressIndicator())
            : Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_adding || _refreshing) const LinearProgressIndicator(),
                  if (_snapshot.missing > 0)
                    Padding(
                      padding: const EdgeInsets.all(12),
                      child: Text(
                        '当前下载目录中 ${_snapshot.missing} 条旧记录对应的文件缺失或不匹配，添加剧集时已过滤；原任务记录保留。',
                      ),
                    ),
                  if (_error != null || widget.library.merges.error.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.all(16),
                      child: Text(_error ?? widget.library.merges.error),
                    ),
                  Expanded(
                    child: jobs.isEmpty
                        ? StatusPanel(
                            title: '还没有合并任务',
                            message: '选择多部剧加入队列，依次合并已下载的连续分集。',
                            secondaryAction: FilledButton.icon(
                              onPressed: _adding ? null : _add,
                              icon: const Icon(Icons.add_rounded),
                              label: const Text('添加剧集'),
                            ),
                          )
                        : ListView.builder(
                            padding: const EdgeInsets.all(12),
                            itemCount: jobs.length,
                            itemBuilder: (_, index) {
                              final job = jobs[index];
                              final processing =
                                  job.state == 'running' ||
                                  job.state == 'cleanup';
                              return Card(
                                child: Padding(
                                  padding: const EdgeInsets.all(16),
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.stretch,
                                    children: [
                                      Text(
                                        job.drama.title,
                                        style: Theme.of(
                                          context,
                                        ).textTheme.titleMedium,
                                      ),
                                      const SizedBox(height: 6),
                                      Text(
                                        '第 ${job.episodes.first}–${job.episodes.last} 集 · ${job.label}${job.cleanup ? ' · 完成后清理分集' : ''}',
                                      ),
                                      if (processing) ...[
                                        const SizedBox(height: 10),
                                        LinearProgressIndicator(
                                          value: widget.library.progress > 0
                                              ? widget.library.progress
                                              : null,
                                        ),
                                        const SizedBox(height: 6),
                                        Text(widget.library.status),
                                      ],
                                      if (job.error.isNotEmpty)
                                        Padding(
                                          padding: const EdgeInsets.only(
                                            top: 8,
                                          ),
                                          child: Text(job.error),
                                        ),
                                      Wrap(
                                        spacing: 8,
                                        children: [
                                          if (job.state == 'completed')
                                            FilledButton.icon(
                                              key: ValueKey(
                                                'merge-output:${job.id}',
                                              ),
                                              onPressed: () => _openOutput(job),
                                              icon: const Icon(
                                                Icons.video_library_outlined,
                                              ),
                                              label: const Text('查看合并成品'),
                                            ),
                                          if (job.active)
                                            TextButton.icon(
                                              onPressed: () =>
                                                  _control(job, 'pause'),
                                              icon: const Icon(
                                                Icons.pause_rounded,
                                              ),
                                              label: const Text('暂停'),
                                            ),
                                          if (job.state == 'paused' ||
                                              job.state == 'failed')
                                            TextButton.icon(
                                              onPressed: () =>
                                                  _control(job, 'resume'),
                                              icon: const Icon(
                                                Icons.play_arrow_rounded,
                                              ),
                                              label: Text(
                                                job.state == 'failed'
                                                    ? '重试'
                                                    : '继续',
                                              ),
                                            ),
                                          if (job.state != 'completed' &&
                                              job.state != 'cancelled')
                                            TextButton(
                                              onPressed: () =>
                                                  _control(job, 'cancel'),
                                              child: const Text('取消任务'),
                                            ),
                                          if (job.state == 'completed' ||
                                              job.state == 'cancelled')
                                            TextButton(
                                              onPressed: () =>
                                                  _control(job, 'forget'),
                                              child: const Text('清理记录'),
                                            ),
                                        ],
                                      ),
                                    ],
                                  ),
                                ),
                              );
                            },
                          ),
                  ),
                ],
              ),
      );
    },
  );
}
