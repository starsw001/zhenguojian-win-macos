import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'playback_routes_bar.dart';

class PlaybackRoutesHeader extends StatelessWidget
    implements PreferredSizeWidget {
  PlaybackRoutesHeader({
    super.key,
    required this.title,
    required this.routes,
    required this.width,
    required this.textScaler,
    required this.titleStyle,
    required this.textDirection,
    required this.onRotate,
  }) : layout = PlaybackHeaderLayout.measure(
         title: title,
         bar: routes,
         width: width,
         textScaler: textScaler,
         titleStyle: titleStyle,
         textDirection: textDirection,
       );

  final String title;
  final PlaybackRoutesBar? routes;
  final double width;
  final TextScaler textScaler;
  final TextStyle titleStyle;
  final TextDirection textDirection;
  final VoidCallback onRotate;
  final PlaybackHeaderLayout layout;

  @override
  Size get preferredSize =>
      Size.fromHeight(layout.toolbarHeight + layout.overflowHeight);

  @override
  Widget build(BuildContext context) {
    final items = routes?.headerItems(layout.scale) ?? const <Widget>[];
    return AppBar(
      centerTitle: false,
      titleSpacing: 16,
      leadingWidth: 56,
      toolbarHeight: layout.toolbarHeight,
      title: Row(
        children: [
          SizedBox(
            width: layout.titleWidth,
            child: Text(
              title,
              key: const ValueKey('playback-header-title'),
              style: titleStyle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (layout.inlineCount > 0) ...[
            const SizedBox(width: 12),
            for (var i = 0; i < layout.inlineCount; i++) ...[
              if (i > 0) const SizedBox(width: 6),
              items[i],
            ],
          ],
        ],
      ),
      bottom: layout.overflowHeight == 0
          ? null
          : PreferredSize(
              preferredSize: Size.fromHeight(layout.overflowHeight),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: SizedBox(
                  width: math.max(0, width - 24),
                  height: layout.overflowHeight,
                  child: SingleChildScrollView(
                    key: const ValueKey('playback-header-overflow'),
                    child: Wrap(
                      spacing: 6,
                      runSpacing: 0,
                      children: [
                        for (final item in items.skip(layout.inlineCount))
                          ConstrainedBox(
                            constraints: BoxConstraints(
                              maxWidth: math.max(0, width - 24),
                            ),
                            child: item,
                          ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
      actions: [
        SizedBox(
          width: 48,
          child: IconButton(
            tooltip: '旋转与全屏',
            onPressed: onRotate,
            icon: const Icon(Icons.screen_rotation_alt_rounded),
          ),
        ),
      ],
    );
  }
}

class PlaybackHeaderLayout {
  const PlaybackHeaderLayout({
    required this.titleWidth,
    required this.inlineCount,
    required this.toolbarHeight,
    required this.overflowHeight,
    required this.scale,
  });

  final double titleWidth;
  final int inlineCount;
  final double toolbarHeight;
  final double overflowHeight;
  final double scale;

  static PlaybackHeaderLayout measure({
    required String title,
    required PlaybackRoutesBar? bar,
    required double width,
    required TextScaler textScaler,
    required TextStyle titleStyle,
    required TextDirection textDirection,
  }) {
    final scale = textScaler.scale(12) / 12;
    final itemHeight = 44 * scale;
    final available = math.max(0.0, width - 136);
    final painter = TextPainter(
      text: TextSpan(text: title, style: titleStyle),
      textDirection: textDirection,
      textScaler: textScaler,
      maxLines: 1,
    )..layout();
    final titleWidth = math.min(
      available,
      math.min(painter.width.ceilToDouble() + 2, 320 * scale),
    );
    painter.dispose();
    final widths = bar?.headerItemWidths(scale) ?? const <double>[];
    var remaining = math.max(0.0, available - titleWidth - 12);
    var inlineCount = 0;
    for (final item in widths) {
      final required = item + (inlineCount == 0 ? 0 : 6);
      if (required > remaining) break;
      remaining -= required;
      inlineCount++;
    }
    final overflowWidth = math.max(1.0, width - 24);
    var rows = 0;
    var used = 0.0;
    for (final raw in widths.skip(inlineCount)) {
      final item = math.min(overflowWidth, raw);
      if (rows == 0 || used + 6 + item > overflowWidth) {
        rows++;
        used = item;
      } else {
        used += 6 + item;
      }
    }
    return PlaybackHeaderLayout(
      titleWidth: titleWidth,
      inlineCount: inlineCount,
      toolbarHeight: math.max(kToolbarHeight, itemHeight),
      overflowHeight: math.min(rows, inlineCount > 0 ? 1 : 2) * itemHeight,
      scale: scale,
    );
  }
}
