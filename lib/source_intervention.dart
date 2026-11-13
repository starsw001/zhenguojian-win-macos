import 'dart:async';

import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import 'source_status.dart';

class SourceIntervention {
  SourceIntervention(this.status);

  final SourceStatus status;
  bool get rateLimited =>
      status.error.contains('429') || status.error.contains('限流');
  bool get challenge => const [
    '验证码',
    '浏览器验证',
    '安全验证',
    'cloudflare',
  ].any(status.error.toLowerCase().contains);
  bool get required => rateLimited || challenge;
  bool canResumeAt(DateTime now) =>
      !status.running &&
      (status.retryAt == null || !now.isBefore(status.retryAt!));

  Uri? get verificationUri {
    final host = RegExp(
      r'(?:https?://)?([a-zA-Z0-9](?:[a-zA-Z0-9-]*\.)+[a-zA-Z]{2,})(?=[: /])',
    ).firstMatch(status.error)?.group(1);
    if (host != null) return Uri.https(host, '/');
    final origin = Uri.tryParse(status.catalogOrigin);
    if (origin == null ||
        !const ['https', 'http'].contains(origin.scheme) ||
        origin.host.isEmpty ||
        origin.userInfo.isNotEmpty) {
      return null;
    }
    return Uri(
      scheme: origin.scheme,
      host: origin.host,
      port: origin.hasPort ? origin.port : null,
      path: '/',
    );
  }
}

Future<bool> showSourceIntervention(
  BuildContext context, {
  required String name,
  required SourceStatus status,
}) async =>
    await showDialog<bool>(
      context: context,
      builder: (_) => SourceInterventionDialog(name: name, status: status),
    ) ??
    false;

class SourceInterventionDialog extends StatefulWidget {
  const SourceInterventionDialog({
    super.key,
    required this.name,
    required this.status,
  });
  final String name;
  final SourceStatus status;

  @override
  State<SourceInterventionDialog> createState() =>
      _SourceInterventionDialogState();
}

class _SourceInterventionDialogState extends State<SourceInterventionDialog> {
  Timer? _timer;
  String _launchError = '';

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _open(Uri uri) async {
    try {
      if (!await launchUrl(uri, mode: LaunchMode.externalApplication)) {
        throw StateError('未找到可用浏览器');
      }
    } catch (_) {
      if (mounted) {
        setState(() => _launchError = '无法打开浏览器，请复制上方地址手动打开；未安装任何组件。');
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final intervention = SourceIntervention(widget.status);
    final uri = intervention.verificationUri;
    final seconds = widget.status.retrySeconds;
    return AlertDialog(
      title: Text(
        '${widget.name} · ${intervention.rateLimited ? '等待限流冷却' : '需要人工验证'}',
      ),
      content: SizedBox(
        width: 480,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SelectableText(widget.status.error),
              const SizedBox(height: 12),
              Text(
                intervention.rateLimited
                    ? '目录和任务记录保留，冷却期间不发送重试，也不切换域名规避限流。'
                    : '请在源站原始验证页面手动填写验证码。软件不会代答，也不会通过切换域名绕过验证。',
              ),
              if (uri != null) ...[
                const SizedBox(height: 8),
                SelectableText(uri.toString()),
                TextButton.icon(
                  onPressed: () => _open(uri),
                  icon: const Icon(Icons.open_in_browser),
                  label: const Text('在浏览器打开验证页'),
                ),
              ],
              const Text(
                '当前未集成网页验证码控件，验证由默认浏览器完成；浏览器登录态不会自动复制给软件。若站点要求共享会话，仍需其支持的合法访问方式。',
              ),
              if (seconds > 0) Text('还需等待 $seconds 秒'),
              if (_launchError.isNotEmpty) Text(_launchError),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('稍后处理，保留记录'),
        ),
        FilledButton(
          key: const ValueKey('source-human-resume'),
          onPressed: intervention.canResumeAt(DateTime.now())
              ? () => Navigator.pop(context, true)
              : null,
          child: Text(intervention.rateLimited ? '冷却后继续' : '已手动验证，继续'),
        ),
      ],
    );
  }
}
