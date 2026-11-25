import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';
import 'package:url_launcher/url_launcher.dart';

import 'core_bridge.dart';
import 'local_store.dart';

bool soraniLoginRequired(String? message) =>
    message != null && message.contains('青空') && message.contains('登录');

String soraniSourceError(String message, {required bool authenticated}) =>
    !authenticated && message.contains('青空') && message.contains('暂无可用播放地址')
    ? '青空需要登录，请点击“登录青空账号”；没有账号请先到源站自行注册。'
    : message;

bool soraniLoginOrigin(Uri? uri) =>
    uri != null &&
    uri.scheme == 'https' &&
    uri.host == 'www.sorani.net' &&
    (!uri.hasPort || uri.port == 443) &&
    uri.userInfo.isEmpty;

Future<bool> openSoraniLogin(
  BuildContext context, {
  required AppRepository repository,
  required LocalStore store,
}) async =>
    await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => SoraniLoginScreen(repository: repository, store: store),
      ),
    ) ??
    false;

class SoraniLoginScreen extends StatefulWidget {
  const SoraniLoginScreen({
    super.key,
    required this.repository,
    required this.store,
  });

  final AppRepository repository;
  final LocalStore store;

  @override
  State<SoraniLoginScreen> createState() => _SoraniLoginScreenState();
}

class _SoraniLoginScreenState extends State<SoraniLoginScreen> {
  InAppWebViewController? _controller;
  WebViewEnvironment? _environment;
  late final int _profileEpoch;
  bool _ready = false, _busy = false, _connected = false;
  double _progress = 0;
  String _error = '', _notice = '';
  bool _missingRuntime = false;

  bool get _allowed =>
      widget.store.profileEpoch == _profileEpoch &&
      widget.store.allowsSource('sorani') &&
      !widget.store.locked;

  @override
  void initState() {
    super.initState();
    _profileEpoch = widget.store.profileEpoch;
    unawaited(_initialize());
  }

  Future<void> _initialize() async {
    try {
      if (!_allowed) throw AppFailure('当前用户无权访问青空，请返回重新操作');
      final status = await widget.repository.sourceAccount('sorani', 'status');
      if (!mounted) return;
      _connected = status['authenticated'] == true;
      _notice = status['message'] as String? ?? '';
      if (Platform.isWindows) {
        if (await WebViewEnvironment.getAvailableVersion() == null) {
          _missingRuntime = true;
          throw AppFailure(
            '缺少 Microsoft Edge WebView2 Runtime，无法显示青空原站登录页。请自行安装微软官方运行时后重试；软件不会自动安装组件。',
          );
        }
        final support = await getApplicationSupportDirectory();
        final folder = Directory(path.join(support.path, 'webviews', 'sorani'));
        if (await FileSystemEntity.isLink(folder.path)) {
          throw AppFailure('青空网页登录目录不能是链接');
        }
        await folder.create(recursive: true);
        final environment = await WebViewEnvironment.create(
          settings: WebViewEnvironmentSettings(userDataFolder: folder.path),
        );
        if (!mounted || !_allowed) {
          await environment.dispose();
          return;
        }
        _environment = environment;
      }
      if (mounted && _allowed) setState(() => _ready = true);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  @override
  void dispose() {
    final environment = _environment;
    if (environment != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        unawaited(environment.dispose());
      });
    }
    super.dispose();
  }

  Future<void> _openOriginalLogin() async {
    final controller = _controller;
    if (controller == null || !_allowed) return;
    final uri = await controller.getUrl();
    if (!soraniLoginOrigin(uri)) return;
    await controller.evaluateJavascript(
      source: '''
(() => {
  for (const element of document.querySelectorAll('button, a')) {
    const text = element.textContent.trim();
    if (['登录', 'Login', 'Log in', 'Sign in'].includes(text) && element.getClientRects().length) {
      element.click();
      return true;
    }
  }
  return false;
})()
''',
    );
  }

  Future<void> _connect() async {
    if (_busy || !_allowed) return;
    final controller = _controller;
    if (controller == null) return;
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      if (!soraniLoginOrigin(await controller.getUrl())) {
        throw AppFailure('只能连接青空原站登录页中的账号');
      }
      final raw = await controller.evaluateJavascript(
        source: '''
(() => {
  if (location.origin !== 'https://www.sorani.net') return null;
  try {
    const session = JSON.parse(localStorage.getItem('sorani_auth_token') || 'null');
    return session ? JSON.stringify({accessToken: session.accessToken, refreshToken: session.refreshToken, userAgent: navigator.userAgent}) : null;
  } catch (_) { return null; }
})()
''',
      );
      if (!_allowed) throw AppFailure('当前用户已变化，请返回重新登录');
      final decoded = raw is String ? jsonDecode(raw) : raw;
      if (decoded is! Map ||
          decoded['accessToken'] is! String ||
          decoded['refreshToken'] is! String) {
        throw AppFailure(
          '青空需要登录：请先在下方原站页面填写账号、密码并手动完成验证码，再点击“完成登录并连接软件”。没有账号请先自行注册。',
        );
      }
      final status = await widget.repository.sourceAccount(
        'sorani',
        'connect',
        session: {
          'accessToken': decoded['accessToken'],
          'refreshToken': decoded['refreshToken'],
          if (decoded['userAgent'] is String) 'userAgent': decoded['userAgent'],
        },
      );
      if (!mounted || !_allowed) return;
      setState(() {
        _connected = status['authenticated'] == true;
        _notice = status['message'] as String? ?? '已连接青空账号';
      });
      if (_connected) Navigator.of(context).pop(true);
    } on FormatException {
      if (mounted) setState(() => _error = '未取得有效青空登录状态，请先完成原站登录后重试。');
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _logout() async {
    if (_busy || !_allowed) return;
    setState(() => _busy = true);
    try {
      await widget.repository.sourceAccount('sorani', 'logout');
      final controller = _controller;
      if (controller != null && soraniLoginOrigin(await controller.getUrl())) {
        await controller.evaluateJavascript(
          source:
              "localStorage.removeItem('sorani_auth_token'); sessionStorage.removeItem('sorani_auth_token'); location.reload();",
        );
      }
      if (mounted) {
        setState(() {
          _connected = false;
          _notice = '已断开本机青空账号，重新播放需要登录。';
        });
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _openExternal(Uri uri) async {
    try {
      if (!await launchUrl(uri, mode: LaunchMode.externalApplication)) {
        throw AppFailure('无法打开浏览器');
      }
    } catch (_) {
      if (mounted) setState(() => _error = '无法打开浏览器，请手动打开 ${uri.origin}。');
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('青空账号登录')),
    body: SafeArea(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('请在下方青空原站页面自行登录并完成验证码。没有账号，请先到源站自行注册，再回来登录。'),
                const SizedBox(height: 6),
                Text(
                  Platform.isWindows
                      ? '密码由青空原站直接处理，不经过本地核心；确认连接后，仅在当前 Windows 用户下加密保存会话，用于本机青空访问。'
                      : '密码由青空原站直接处理；确认连接后，会话仅保留在本次运行内存中，关闭软件后需要重新登录。',
                ),
                if (_notice.isNotEmpty) Text(_notice),
                if (_error.isNotEmpty)
                  SelectableText(
                    _error,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 10,
                  runSpacing: 8,
                  children: [
                    FilledButton.icon(
                      key: const ValueKey('sorani-connect-account'),
                      onPressed: _ready && !_busy && _allowed ? _connect : null,
                      icon: const Icon(Icons.login),
                      label: Text(_busy ? '正在连接…' : '完成登录并连接软件'),
                    ),
                    OutlinedButton(
                      onPressed: _ready && !_busy ? _openOriginalLogin : null,
                      child: const Text('显示原站登录框'),
                    ),
                    TextButton(
                      onPressed: () =>
                          _openExternal(Uri.parse('https://www.sorani.net/')),
                      child: const Text('没有账号？到青空原站自行注册'),
                    ),
                    if (_connected)
                      TextButton(
                        onPressed: _busy ? null : _logout,
                        child: const Text('退出本机青空账号'),
                      ),
                    if (_missingRuntime)
                      TextButton(
                        onPressed: () => _openExternal(
                          Uri.parse(
                            'https://developer.microsoft.com/microsoft-edge/webview2/',
                          ),
                        ),
                        child: const Text('微软官方 WebView2 下载页'),
                      ),
                    if (_ready)
                      IconButton(
                        onPressed: _busy ? null : () => _controller?.reload(),
                        tooltip: '刷新原站页面',
                        icon: const Icon(Icons.refresh),
                      ),
                  ],
                ),
              ],
            ),
          ),
          if (_ready && _progress < 1)
            LinearProgressIndicator(value: _progress),
          Expanded(
            child: !_ready
                ? Center(
                    child: _error.isEmpty
                        ? const CircularProgressIndicator()
                        : const Text('青空原站登录页尚未就绪'),
                  )
                : InAppWebView(
                    webViewEnvironment: _environment,
                    initialUrlRequest: URLRequest(
                      url: WebUri('https://www.sorani.net/'),
                    ),
                    initialSettings: InAppWebViewSettings(
                      incognito: true,
                      isInspectable: false,
                      useShouldOverrideUrlLoading: true,
                      mediaPlaybackRequiresUserGesture: true,
                      javaScriptCanOpenWindowsAutomatically: false,
                      supportMultipleWindows: false,
                    ),
                    onWebViewCreated: (controller) => _controller = controller,
                    onLoadStop: (_, _) async => _openOriginalLogin(),
                    onProgressChanged: (_, progress) {
                      if (mounted) setState(() => _progress = progress / 100);
                    },
                    shouldOverrideUrlLoading: (_, action) async {
                      if (action.isForMainFrame &&
                          !soraniLoginOrigin(action.request.url)) {
                        if (mounted) {
                          setState(() => _error = '此窗口仅用于青空原站登录，已阻止跳转到其他站点。');
                        }
                        return NavigationActionPolicy.CANCEL;
                      }
                      return NavigationActionPolicy.ALLOW;
                    },
                    onReceivedError: (_, request, _) {
                      if (request.isForMainFrame == true && mounted) {
                        setState(
                          () => _error = '青空原站登录页加载失败，请检查网络后点击刷新；不会绕过证书或安全验证。',
                        );
                      }
                    },
                  ),
          ),
        ],
      ),
    ),
  );
}
