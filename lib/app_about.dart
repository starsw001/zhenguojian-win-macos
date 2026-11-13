import 'package:flutter/material.dart';

import 'app_build.dart';
import 'app_version.g.dart';
import 'core_bridge.dart';
import 'updater.dart';

class AppAboutDialog extends StatelessWidget {
  const AppAboutDialog({super.key, required this.repository});

  final AppRepository repository;

  @override
  Widget build(BuildContext context) => AlertDialog(
    scrollable: true,
    title: const Row(
      children: [
        Icon(
          Icons.play_circle_filled_rounded,
          size: 48,
          color: Color(0xFFFF765F),
        ),
        SizedBox(width: 20),
        Flexible(child: Text(appName)),
      ],
    ),
    content: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('独立运行，打开即可浏览和播放。观看记录与追剧收藏保存在当前设备。'),
        _CheckUpdatesButton(repository: repository),
        const SizedBox(height: 12),
        const Text(
          '免责声明\n'
          '本应用为第三方聚合客户端，与任何官方应用无关。\n'
          '应用本身不存储、不传播任何视频内容，所有内容均来自第三方站点，版权归各权利人所有；请遵守所在地法律法规，因使用本应用产生的一切后果由使用者自行承担。',
          style: TextStyle(fontSize: 12, height: 1.5),
        ),
      ],
    ),
    actions: [
      if (appMaintenanceEntriesVisible)
        TextButton(
          onPressed: () =>
              showLicensePage(context: context, applicationName: appName),
          child: const Text('查看许可'),
        ),
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  );
}

class _CheckUpdatesButton extends StatefulWidget {
  const _CheckUpdatesButton({required this.repository});
  final AppRepository repository;
  @override
  State<_CheckUpdatesButton> createState() => _CheckUpdatesButtonState();
}

class _CheckUpdatesButtonState extends State<_CheckUpdatesButton> {
  bool checking = false;
  Future<void> check() async {
    if (checking) return;
    setState(() => checking = true);
    try {
      UpdateInfo? info;
      var reason = '无法完成更新检查，请检查网络后再试。';
      try {
        info = await checkForUpdate(widget.repository);
      } on AppFailure catch (e) {
        reason = e.message;
      }
      if (!mounted) return;
      await showUpdateDialog(
        context,
        widget.repository,
        info ??
            UpdateInfo(
              available: false,
              current: appVersion,
              version: '',
              notes: '',
              pageUrl: '',
              assetUrl: '',
              error: reason,
            ),
        manual: true,
      );
    } finally {
      if (mounted) setState(() => checking = false);
    }
  }

  @override
  Widget build(BuildContext context) => TextButton.icon(
    key: const ValueKey('about-check-updates'),
    onPressed: checking ? null : check,
    icon: const Icon(Icons.system_update_alt_rounded, size: 18),
    label: Text(checking ? '正在检查…' : '检查更新'),
  );
}
