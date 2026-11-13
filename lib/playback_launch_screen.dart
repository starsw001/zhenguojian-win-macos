import 'update_activity.dart';
import 'dart:async';

import 'package:flutter/material.dart';

import 'core_bridge.dart';
import 'follow_state.dart';
import 'local_store.dart';
import 'models.dart';
import 'player_screen.dart';
import 'widgets.dart';
import 'ordinary_catalog.dart';
import 'ordinary_site_selector.dart';

final _playbackLaunches = Expando<bool>();

Future<void> openPlaybackDirectly(
  BuildContext context, {
  required Drama drama,
  required AppRepository repository,
  required LocalStore store,
}) async {
  final navigator = Navigator.of(context);
  if (_playbackLaunches[navigator] == true) return;
  _playbackLaunches[navigator] = true;
  try {
    await navigator.push<void>(
      MaterialPageRoute<void>(
        builder: (_) => PlaybackLaunchScreen(
          drama: drama,
          repository: repository,
          store: store,
        ),
      ),
    );
  } finally {
    _playbackLaunches[navigator] = null;
  }
}

class PlaybackLaunchScreen extends StatefulWidget {
  const PlaybackLaunchScreen({
    super.key,
    required this.drama,
    required this.repository,
    required this.store,
    this.initialDetail,
    this.initialEpisode,
    this.initialPosition,
    this.playerBuilder,
  });
  final Drama drama;
  final AppRepository repository;
  final LocalStore store;
  final DramaDetail? initialDetail;
  final int? initialEpisode;
  final double? initialPosition;
  @visibleForTesting
  final Widget Function(DramaDetail, int, double, Widget?)? playerBuilder;
  @override
  State<PlaybackLaunchScreen> createState() => _PlaybackLaunchScreenState();
}

class _PlaybackLaunchScreenState extends State<PlaybackLaunchScreen> {
  late final int _profileEpoch;
  late Drama _selected;
  int _generation = 0;
  String? _error;
  bool _loading = false, _initialUsed = false;
  DramaDetail? _detail;
  int _initialIndex = 0;
  double _initialPosition = 0;
  List<Drama> get _sites =>
      visibleOrdinarySites(widget.drama, widget.repository, widget.store);
  Widget? get _selector {
    final choices = _sites;
    return choices.length < 2
        ? null
        : OrdinarySiteSelector(
            sites: choices,
            selected: _selected,
            onSelected: (site) {
              if (site.siteIdentity != _selected.siteIdentity) {
                unawaited(_load(site: site));
              }
            },
          );
  }

  @override
  void initState() {
    super.initState();
    UpdateActivity.enterPlayer();
    _selected = widget.drama;
    _profileEpoch = widget.store.profileEpoch;
    widget.store.addListener(_profileChanged);
    unawaited(_load());
  }

  @override
  void dispose() {
    UpdateActivity.leavePlayer();
    _generation++;
    widget.store.removeListener(_profileChanged);
    super.dispose();
  }

  void _profileChanged() {
    if (_profileEpoch != widget.store.profileEpoch && _detail == null) {
      _generation++;
      setState(() => _error = '用户已切换，请返回重新选择');
    } else if (mounted) {
      setState(() {});
    }
  }

  Future<void> _load({Drama? site}) async {
    if (_loading && site == null) return;
    if (_profileEpoch != widget.store.profileEpoch || widget.store.locked) {
      setState(() => _error = '用户已切换，请返回重新选择');
      return;
    }
    final selected = site ?? _selected;
    if (!widget.store.allowsSource(selected.source)) {
      setState(() => _error = '当前用户无权访问此站点');
      return;
    }
    final initial = !_initialUsed;
    _initialUsed = true;
    _loading = true;
    final generation = ++_generation;
    setState(() {
      _selected = selected;
      _detail = null;
      _error = null;
    });
    try {
      final detail = initial && widget.initialDetail != null
          ? validateIsolatedSiteDetail(selected, widget.initialDetail!)
          : await loadIsolatedSiteDetail(widget.repository, selected);
      if (!mounted ||
          generation != _generation ||
          _profileEpoch != widget.store.profileEpoch) {
        return;
      }
      final drama = widget.repository.catalogUpdates
          .current(selected)
          .merge(detail.drama);
      final merged = DramaDetail(
        drama,
        detail.episodes,
        warning: detail.warning,
      );
      widget.repository.catalogUpdates.publish(drama, retryCover: true);
      await saveUserChange(context, () => widget.store.refreshDrama(drama));
      if (!mounted ||
          generation != _generation ||
          _profileEpoch != widget.store.profileEpoch) {
        return;
      }
      if (merged.episodes.isEmpty) {
        setState(() => _error = '暂时没有可播放的集数');
        return;
      }
      final watched = widget.store.watched(drama.id);
      final wanted = initial && widget.initialEpisode != null
          ? merged.episodes.indexWhere(
              (episode) => episode.number == widget.initialEpisode,
            )
          : -1;
      final index = wanted >= 0
          ? wanted
          : resumeEpisodeIndex(merged.episodes, watched);
      final episode = merged.episodes[index];
      if (episode.vip &&
          drama.source != SourceSite.dsd.id &&
          !(initial && widget.initialDetail != null)) {
        final accepted = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('这是一集 VIP 内容'),
            content: const Text('站源可能只提供试看或限制播放。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('尝试播放'),
              ),
            ],
          ),
        );
        if (!mounted || generation != _generation) return;
        if (accepted != true) {
          Navigator.of(context).maybePop();
          return;
        }
      }
      final position = initial && widget.initialPosition != null
          ? widget.initialPosition!
          : watched?.episode == episode.number && watched?.finished == false
          ? watched!.position
          : 0.0;
      if (!mounted ||
          generation != _generation ||
          _profileEpoch != widget.store.profileEpoch) {
        return;
      }
      setState(() {
        _detail = merged;
        _initialIndex = index;
        _initialPosition = position;
      });
    } catch (error) {
      if (!mounted || generation != _generation) return;
      setState(() => _error = error.toString());
      if (!SourceSite.ordinaryIds.contains(selected.source)) {
        widget.repository.catalogUpdates.publish(
          widget.repository.catalogUpdates.current(selected),
          retryCover: true,
        );
      }
    } finally {
      if (generation == _generation) _loading = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final selector = _selector;
    if (_detail case final detail?) {
      return widget.playerBuilder?.call(
            detail,
            _initialIndex,
            _initialPosition,
            selector,
          ) ??
          PlayerScreen(
            key: ValueKey(_selected.siteIdentity),
            detail: detail,
            initialIndex: _initialIndex,
            initialPosition: _initialPosition,
            repository: widget.repository,
            store: widget.store,
            siteSelector: selector,
          );
    }
    return Scaffold(
      appBar: AppBar(title: Text(_selected.title)),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            ?selector,
            Expanded(
              child: _error == null
                  ? const Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          CircularProgressIndicator(),
                          SizedBox(height: 16),
                          Text('正在进入播放'),
                        ],
                      ),
                    )
                  : StatusPanel(
                      title: '暂时无法播放',
                      message: _error!,
                      onRetry: () => _load(),
                      action: '重试',
                      icon: Icons.play_disabled_rounded,
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
