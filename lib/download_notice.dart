import 'package:flutter/material.dart';

const downloadQueuedNoticeDuration = Duration(seconds: 10);

void showDownloadQueuedNotice(
  BuildContext context, {
  required int added,
  required VoidCallback onView,
}) {
  final messenger = ScaffoldMessenger.of(context);
  final colors = Theme.of(context).colorScheme;
  final buttonStyle = TextButton.styleFrom(
    foregroundColor:
        Theme.of(context).snackBarTheme.actionTextColor ??
        colors.inversePrimary,
    minimumSize: const Size(48, 36),
    padding: const EdgeInsets.symmetric(horizontal: 8),
    visualDensity: VisualDensity.compact,
  );
  messenger.hideCurrentSnackBar();
  messenger.showSnackBar(
    SnackBar(
      duration: downloadQueuedNoticeDuration,
      content: Row(
        children: [
          Expanded(
            child: Text(
              added == 0 ? '所选集数已在下载列表中' : '已加入 $added 集，已有任务自动跳过',
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          TextButton(
            key: const ValueKey('download-notice-view'),
            style: buttonStyle,
            onPressed: () {
              messenger.hideCurrentSnackBar();
              onView();
            },
            child: const Text('查看'),
          ),
          TextButton(
            key: const ValueKey('download-notice-close'),
            style: buttonStyle,
            onPressed: messenger.hideCurrentSnackBar,
            child: const Text('关闭'),
          ),
        ],
      ),
    ),
  );
}
