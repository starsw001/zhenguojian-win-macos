import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';

import 'core_bridge.dart';
import 'local_store.dart';

const piguoBrowserUserAgent =
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 '
    '(KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36';

bool piguoAccessOrigin(Uri? uri) =>
    uri != null &&
    uri.scheme == 'https' &&
    const {'ptt.red', 'www.ptt.red'}.contains(uri.host) &&
    (!uri.hasPort || uri.port == 443) &&
    uri.userInfo.isEmpty;

Future<bool> openPiguoAccess(
  BuildContext context, {
  required AppRepository repository,
  required LocalStore store,
}) async =>
    await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => PiguoAccessScreen(repository: repository, store: store),
      ),
    ) ??
    false;

class PiguoAccessScreen extends StatefulWidget {
  const PiguoAccessScreen({
    super.key,
    required this.repository,
    required this.store,
  });

  final AppRepository repository;
  final LocalStore store;

  @override
  State<PiguoAccessScreen> createState() => _PiguoAccessScreenState();
}

class _PiguoAccessScreenState extends State<PiguoAccessScreen> {
  static final _origin = WebUri('https://ptt.red/');
  static final _catalog = WebUri('https://ptt.red/p/66/c/67');
  static final _cookieNames = RegExp(
    r'^(?:cf_clearance|__cf_bm|_cfuvid|cf_chl_)',
  );

  InAppWebViewController? _controller;
  WebViewEnvironment? _environment;
  late final int _profileEpoch;
  bool _ready = false, _busy = false, _connected = false;
  bool _missingRuntime = false;
  double _progress = 0;
  String _error = '', _notice = '';

  bool get _allowed =>
      widget.store.profileEpoch == _profileEpoch &&
      widget.store.allowsSource('piguo') &&
      !widget.store.locked;

  @override
  void initState() {
    super.initState();
    _profileEpoch = widget.store.profileEpoch;
    unawaited(_initialize());
  }

  Future<void> _initialize() async {
    try {
      if (!_allowed) throw AppFailure('当前用户无权访问皮果，请返回重新操作');
      final status = await widget.repository.sourceAccount('piguo', 'status');
      if (!mounted) return;
      _connected = status['authenticated'] == true;
      _notice = status['message'] as String? ?? '';
      if (Platform.isWindows) {
        if (await WebViewEnvironment.getAvailableVersion() == null) {
          _missingRuntime = true;
          throw AppFailure(
            '缺少 Microsoft Edge WebView2 Runtime，无法显示皮果原站验证页。请安装微软官方运行时后重试；软件不会自动安装组件。',
          );
        }
        final support = await getApplicationSupportDirectory();
        final folder = Directory(path.join(support.path, 'webviews', 'piguo'));
        if (await FileSystemEntity.isLink(folder.path)) {
          throw AppFailure('皮果网页验证目录不能是链接');
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

  Future<void> _connect() async {
    if (_busy || !_allowed) return;
    final controller = _controller;
    if (controller == null) return;
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      if (!piguoAccessOrigin(await controller.getUrl())) {
        throw AppFailure('只能连接皮果原站验证页');
      }
      final raw = await controller.evaluateJavascript(
        source: '''
(() => JSON.stringify({
  title: document.title || '',
  challenge: /just a moment|security verification/i.test(document.title || '') ||
    !!document.querySelector('#challenge-error-text, form[action*="challenge"]'),
  cards: document.querySelectorAll('#videos a[href]').length
}))()
''',
      );
      final page = raw is String ? jsonDecode(raw) : raw;
      if (page is! Map || page['challenge'] == true) {
        throw AppFailure('皮果原站验证尚未完成，请在下方页面手动完成后重试');
      }
      final manager = CookieManager.instance(
        webViewEnvironment: Platform.isWindows ? _environment : null,
      );
      final cookies = await manager.getCookies(
        url: _origin,
        webViewController: controller,
      );
      final verified = cookies
          .where((cookie) => _cookieNames.hasMatch(cookie.name))
          .map((cookie) => '${cookie.name}=${cookie.value}')
          .toList(growable: false);
      if (!verified.any((cookie) => cookie.startsWith('cf_clearance='))) {
        throw AppFailure('皮果原站尚未生成有效验证状态，请等待页面完整打开后重试');
      }
      final status = await widget.repository.sourceAccount(
        'piguo',
        'connect',
        session: {
          'cookie': verified.join('; '),
          'userAgent': piguoBrowserUserAgent,
        },
      );
      if (!mounted || !_allowed) return;
      setState(() {
        _connected = status['authenticated'] == true;
        _notice = status['message'] as String? ?? '已连接皮果原站验证会话';
      });
      if (_connected) Navigator.of(context).pop(true);
    } on FormatException {
      if (mounted) setState(() => _error = '皮果验证页状态无效，请刷新原站页面后重试。');
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _clear() async {
    if (_busy || !_allowed) return;
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      await widget.repository.sourceAccount('piguo', 'logout');
      await CookieManager.instance(
        webViewEnvironment: Platform.isWindows ? _environment : null,
      ).deleteCookies(url: _origin);
      if (mounted) {
        setState(() {
          _connected = false;
          _notice = '已清除皮果本机验证会话，请在下方原站页面重新验证。';
        });
        await _controller?.reload();
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('皮果原站验证')),
    body: SafeArea(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('请在下方皮果原站页面手动完成浏览器验证，看到作品目录后再连接软件。'),
                const SizedBox(height: 6),
                Text(
                  Platform.isWindows
                      ? '软件不读取系统 Chrome/Edge 会话，只接收本窗口的 Cloudflare 验证 Cookie；Windows 使用当前用户 DPAPI 加密保存在 app-data。'
                      : '软件不读取系统浏览器会话，只接收本窗口的 Cloudflare 验证 Cookie；当前平台仅在本次运行内存中交给核心。',
                ),
                const Text('不会代答验证码、不会绕过安全验证，也不会在此窗口启动播放或下载。'),
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
                      key: const ValueKey('piguo-connect-access'),
                      onPressed: _ready && !_busy && _allowed ? _connect : null,
                      icon: const Icon(Icons.verified_user_outlined),
                      label: Text(_busy ? '正在核对…' : '完成验证并连接软件'),
                    ),
                    if (_connected)
                      TextButton(
                        onPressed: _busy ? null : _clear,
                        child: const Text('清除本机验证'),
                      ),
                    if (_ready)
                      IconButton(
                        onPressed: _busy ? null : () => _controller?.reload(),
                        tooltip: '刷新原站页面',
                        icon: const Icon(Icons.refresh),
                      ),
                    if (_missingRuntime) const Text('请先安装 WebView2 Runtime'),
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
                        : const Text('皮果原站验证页尚未就绪'),
                  )
                : InAppWebView(
                    webViewEnvironment: _environment,
                    initialUrlRequest: URLRequest(url: _catalog),
                    initialSettings: InAppWebViewSettings(
                      userAgent: piguoBrowserUserAgent,
                      incognito: false,
                      isInspectable: false,
                      useShouldOverrideUrlLoading: true,
                      mediaPlaybackRequiresUserGesture: true,
                      javaScriptCanOpenWindowsAutomatically: false,
                      supportMultipleWindows: false,
                    ),
                    onWebViewCreated: (controller) => _controller = controller,
                    onLoadStop: (_, uri) async {
                      if (!mounted || !piguoAccessOrigin(uri)) return;
                      final title = await _controller?.getTitle() ?? '';
                      setState(() {
                        _notice = title.toLowerCase().contains('just a moment')
                            ? '等待皮果原站浏览器验证完成。'
                            : '皮果原站页面已打开；确认出现作品目录后点击连接。';
                      });
                    },
                    onProgressChanged: (_, progress) {
                      if (mounted) setState(() => _progress = progress / 100);
                    },
                    shouldOverrideUrlLoading: (_, action) async {
                      if (action.isForMainFrame &&
                          !piguoAccessOrigin(action.request.url)) {
                        if (mounted) {
                          setState(() => _error = '此窗口仅用于皮果原站验证，已阻止跳转到其他站点。');
                        }
                        return NavigationActionPolicy.CANCEL;
                      }
                      return NavigationActionPolicy.ALLOW;
                    },
                    onReceivedError: (_, request, _) {
                      if (request.isForMainFrame == true && mounted) {
                        setState(() => _error = '皮果原站验证页加载失败，请检查网络后点击刷新。');
                      }
                    },
                  ),
          ),
        ],
      ),
    ),
  );
}
