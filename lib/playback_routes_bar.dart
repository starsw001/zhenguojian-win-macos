import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'models.dart';

class PlaybackRoutesBar extends StatelessWidget {
  const PlaybackRoutesBar({
    super.key,
    required this.routes,
    required this.selected,
    required this.busy,
    required this.onSelected,
    this.routesBlocked = false,
    this.local = false,
    this.onSwitchOnline,
    this.onSwitchLocal,
  });
  final List<PlaybackRouteStatus> routes;
  final int selected;
  final bool busy;
  final bool routesBlocked;
  final ValueChanged<int> onSelected;
  final bool local;
  final VoidCallback? onSwitchOnline;
  final VoidCallback? onSwitchLocal;

  List<double> headerItemWidths(double scale) => [
    if (local) ...[
      180 * scale,
      if (onSwitchOnline != null) 188 * scale,
    ] else ...[
      if (onSwitchLocal != null) 184 * scale,
      for (final _ in routes) 100 * scale,
    ],
  ];

  List<Widget> headerItems(double scale) {
    final children = <Widget>[
      if (local) ...[
        const Row(
          children: [
            Icon(Icons.folder_open, size: 20),
            SizedBox(width: 8),
            Expanded(child: Text('正在播放本地视频')),
          ],
        ),
        if (onSwitchOnline != null)
          TextButton.icon(
            key: const ValueKey('playback-switch-online'),
            onPressed: busy ? null : onSwitchOnline,
            icon: const Icon(Icons.cloud_outlined, size: 18),
            label: const Text('切换在线播放'),
          ),
      ] else ...[
        if (onSwitchLocal != null)
          TextButton.icon(
            key: const ValueKey('playback-switch-local'),
            onPressed: busy ? null : onSwitchLocal,
            icon: const Icon(Icons.folder_open, size: 18),
            label: const Text('切回本地视频'),
          ),
        for (final route in routes) _chip(route),
      ],
    ];
    final widths = headerItemWidths(scale);
    return [
      for (var i = 0; i < children.length; i++)
        SizedBox(width: widths[i], height: 44 * scale, child: children[i]),
    ];
  }

  double heightForWidth(double width, double scale) {
    final available = math.max(1.0, width);
    var rows = 0;
    var used = 0.0;
    for (final raw in headerItemWidths(scale)) {
      final item = math.min(available, raw);
      if (rows == 0 || used + 6 + item > available) {
        rows++;
        used = item;
      } else {
        used += 6 + item;
      }
    }
    return math.min(rows, 2) * 44 * scale;
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final scale = MediaQuery.textScalerOf(context).scale(12) / 12;
      final width = constraints.maxWidth;
      return SizedBox(
        height: heightForWidth(width, scale),
        child: SingleChildScrollView(
          key: const ValueKey('playback-route-list'),
          child: Wrap(
            spacing: 6,
            runSpacing: 0,
            children: [
              for (final item in headerItems(scale))
                ConstrainedBox(
                  constraints: BoxConstraints(maxWidth: math.max(0, width)),
                  child: item,
                ),
            ],
          ),
        ),
      );
    },
  );

  Widget _chip(PlaybackRouteStatus route) {
    final color = switch (route.status) {
      'passed' => Colors.green.shade700,
      'failed' => Colors.red.shade700,
      'checking' => Colors.orange.shade800,
      _ => Colors.grey,
    };
    final label = switch (route.status) {
      'passed' => '连接通过',
      'failed' => '连接不通',
      'checking' => '检测中',
      _ => '未检测',
    };
    return Tooltip(
      message:
          '地址 ${route.index + 1} · ${route.quality > 0 ? '${route.quality}P · ' : ''}$label${route.elapsedMs > 0 ? ' · ${route.elapsedMs}ms' : ''}\n${route.message.isEmpty ? '只检测当前集媒体或播放列表开头，不下载整集。授权拒绝、验证码及限流时停止。' : route.message}',
      child: Align(
        alignment: Alignment.centerLeft,
        child: ChoiceChip(
          key: ValueKey('playback-route-${route.index}'),
          selected: selected == route.index,
          showCheckmark: false,
          avatar: Icon(
            route.status == 'passed'
                ? Icons.check_circle
                : route.status == 'failed'
                ? Icons.cancel
                : route.status == 'checking'
                ? Icons.hourglass_top
                : Icons.circle_outlined,
            color: color,
            size: 16,
          ),
          label: Text(
            '地址 ${route.index + 1}',
            style: const TextStyle(fontSize: 12),
          ),
          onSelected: busy || routesBlocked
              ? null
              : (_) => onSelected(route.index),
        ),
      ),
    );
  }
}
