import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import 'app_build.dart';
import 'app_theme.dart';
import 'core_bridge.dart';

typedef LicenseRequest =
    Future<Map<String, dynamic>> Function(
      String action, {
      required String username,
      required String password,
      required String key,
    });

bool _trialNotified = false;
void acknowledgeUpdateFrame() {
  if (_trialNotified ||
      !Platform.isWindows ||
      Platform.environment['GUO_UPDATE_TRIAL'] != '1') {
    return;
  }
  _trialNotified = true;
  WidgetsBinding.instance.addPostFrameCallback((_) async {
    try {
      final result = await requestAppLicense('licenseUpdateReady');
      if (result['installing'] == true) {
        await Future<void>.delayed(const Duration(milliseconds: 250));
        exit(0);
      }
    } catch (_) {
      _trialNotified = false;
    }
  });
}

class AppLicenseController extends ChangeNotifier {
  AppLicenseController({
    LicenseRequest? request,
    bool? enabled,
    DateTime Function()? now,
  }) : _request = request ?? requestAppLicense,
       _now = now ?? DateTime.now,
       enabled = enabled ?? Platform.isWindows;

  final LicenseRequest _request;
  final DateTime Function() _now;
  final bool enabled;
  Timer? _timer;
  Timer? _deadlineTimer;
  Timer? _sessionExpiryTimer;
  Timer? _countdownTimer;
  Timer? _retryCountdownTimer;
  DateTime? _statusReceivedAt;
  DateTime? _sessionExpiry;
  bool _requiresExpiryLogin = false;
  bool _disposed = false;
  bool _opened = false;
  bool busy = false;
  Map<String, dynamic> status = const {'phase': 'loading', 'ready': false};

  bool get ready => !enabled || status['ready'] == true;
  String get phase => status['phase'] as String? ?? 'loading';
  String get message {
    final value = status['message'] as String? ?? '正在检查分发签名与本机授权';
    if (value.startsWith('授权请求等待中')) {
      return requestCoolingDown ? '授权请求正在等待，请按下方倒计时重试。' : '等待已结束，可以再次提交授权请求。';
    }
    return value;
  }

  Future<Map<String, dynamic>> checkNetwork() =>
      _request('licenseNetworkCheck', username: '', password: '', key: '');

  int _retrySeconds(String secondsKey, String deadlineKey) {
    final received = _statusReceivedAt;
    final seconds = status[secondsKey];
    final deadline = status[deadlineKey];
    final Duration remaining;
    if (seconds is num && received != null) {
      remaining =
          Duration(seconds: seconds.toInt()) - _now().difference(received);
    } else if (deadline is num && deadline > 0) {
      remaining = DateTime.fromMillisecondsSinceEpoch(
        deadline.toInt() * 1000,
      ).difference(_now());
    } else {
      return 0;
    }
    return remaining <= Duration.zero
        ? 0
        : (remaining.inMicroseconds / Duration.microsecondsPerSecond).ceil();
  }

  int get retryRemainingSeconds {
    if (ready || phase == 'locked' || phase == 'configuration') return 0;
    final network = _retrySeconds(
      'retryNetworkRemainingSeconds',
      'retryNetworkAt',
    );
    final login = phase == 'login'
        ? _retrySeconds('retryLoginRemainingSeconds', 'retryLoginAt')
        : 0;
    return network > login ? network : login;
  }

  bool get requestCoolingDown => retryRemainingSeconds > 0;
  String get retryCountdown {
    final seconds = retryRemainingSeconds;
    return '${seconds ~/ 60} 分 ${(seconds % 60).toString().padLeft(2, '0')} 秒';
  }

  String get username => status['username'] as String? ?? '';
  String get licenseKey => ready ? status['licenseKey'] as String? ?? '' : '';
  DateTime? get expiresAt {
    final value = status['expiresAt'];
    return value is num && value > 0
        ? DateTime.fromMillisecondsSinceEpoch(value.toInt() * 1000)
        : null;
  }

  Duration? get remaining {
    final seconds = status['remainingSeconds'];
    final received = _statusReceivedAt;
    final expiry = expiresAt;
    if (expiry == null) return null;
    final value = seconds is num && received != null
        ? Duration(seconds: seconds.toInt()) - _now().difference(received)
        : expiry.difference(_now());
    return value.isNegative ? Duration.zero : value;
  }

  bool get expiresSoon {
    final value = remaining;
    return value != null && value < const Duration(days: 3);
  }

  Future<void> open() async {
    if (_opened || !enabled) return;
    _opened = true;
    await perform('licenseStatus');
    if (!_disposed) {
      _timer = Timer.periodic(const Duration(minutes: 1), (_) {
        if (!busy) unawaited(perform('licenseStatus'));
      });
      _countdownTimer = Timer.periodic(const Duration(seconds: 1), (_) {
        if (!_disposed && ready && expiresSoon) notifyListeners();
      });
    }
  }

  Future<void> perform(
    String action, {
    String username = '',
    String password = '',
    String key = '',
  }) async {
    if (busy || _disposed || !enabled) return;
    if (requestCoolingDown &&
        const {
          'licenseActivate',
          'licenseVerify',
          'licenseLogin',
        }.contains(action)) {
      notifyListeners();
      return;
    }
    _checkSessionExpiry();
    final expiryLogin = action == 'licenseLogin' && _requiresExpiryLogin;
    busy = true;
    notifyListeners();
    try {
      final result = await _request(
        action,
        username: username,
        password: password,
        key: key,
      );
      if (!_disposed) {
        _applyStatus(result, expiryLogin: expiryLogin);
      }
      if (!_disposed &&
          !_requiresExpiryLogin &&
          action == 'licenseLogin' &&
          result['phase'] == 'verify') {
        final verified = await _request(
          'licenseStatus',
          username: '',
          password: '',
          key: '',
        );
        if (!_disposed) {
          _applyStatus(verified);
        }
      }
    } catch (_) {
      if (!_disposed && !_requiresExpiryLogin) {
        status = const {
          'phase': 'error',
          'ready': false,
          'message': '授权检查暂未完成，请检查完整安装包和网络后重试。原视频和记录保留。',
        };
      }
    } finally {
      busy = false;
      if (!_disposed) {
        _scheduleDeadline();
        notifyListeners();
      }
    }
  }

  void _applyStatus(Map<String, dynamic> result, {bool expiryLogin = false}) {
    _checkSessionExpiry();
    final phase = result['phase'];
    final terminal = phase == 'locked' || phase == 'configuration';
    if (_requiresExpiryLogin && !expiryLogin && !terminal) return;
    if (terminal || expiryLogin && phase != 'login') {
      _requiresExpiryLogin = false;
    }
    if (terminal || phase == 'login' && _sessionExpiry != null) {
      _sessionExpiryTimer?.cancel();
      _sessionExpiry = null;
      _requiresExpiryLogin = !terminal;
    }
    status = result;
    _statusReceivedAt = _now();
    _retryCountdownTimer?.cancel();
    if (requestCoolingDown) {
      _retryCountdownTimer = Timer.periodic(const Duration(seconds: 1), (
        timer,
      ) {
        if (_disposed || !requestCoolingDown) timer.cancel();
        if (!_disposed) notifyListeners();
      });
    }
    final expiry = expiresAt;
    if (!ready || expiry == null || expiry == _sessionExpiry) return;
    _sessionExpiryTimer?.cancel();
    _sessionExpiry = expiry;
    final delay = expiry.difference(_now());
    if (delay <= Duration.zero) {
      _expireSession();
    } else {
      _sessionExpiryTimer = Timer(delay, () {
        if (_disposed) return;
        _expireSession();
        notifyListeners();
        unawaited(perform('licenseStatus'));
      });
    }
  }

  void _checkSessionExpiry() {
    final expiry = _sessionExpiry;
    if (expiry != null && !_now().isBefore(expiry)) _expireSession();
  }

  void _expireSession() {
    _sessionExpiryTimer?.cancel();
    _deadlineTimer?.cancel();
    _sessionExpiry = null;
    _requiresExpiryLogin = true;
    status = const {
      'ready': false,
      'phase': 'login',
      'message': '许可证已到期，已退出。请重新登录，管理员续期后再联网复核。',
    };
  }

  void _scheduleDeadline() {
    _deadlineTimer?.cancel();
    if (!enabled || !ready) return;
    final refresh = status['refreshAt'];
    final expiry = status['expiresAt'];
    if (refresh is! num || expiry is! num || refresh <= 0 || expiry <= 0) {
      return;
    }
    if (refresh >= expiry) return;
    final delay = DateTime.fromMillisecondsSinceEpoch(
      refresh.toInt() * 1000,
    ).difference(_now());
    if (delay <= Duration.zero) {
      status = {
        ...status,
        'licenseKey': null,
        'ready': false,
        'phase': 'verify',
        'message': '授权时间已过，请重新复核许可证',
      };
      return;
    }
    _deadlineTimer = Timer(delay + const Duration(milliseconds: 100), () {
      if (_disposed) return;
      _checkSessionExpiry();
      if (_requiresExpiryLogin) {
        notifyListeners();
        unawaited(perform('licenseStatus'));
        return;
      }
      status = {
        ...status,
        'licenseKey': null,
        'ready': false,
        'phase': 'verify',
        'message': '授权租约已到复核时间，正在联网检查',
      };
      notifyListeners();
      unawaited(perform('licenseStatus'));
    });
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _deadlineTimer?.cancel();
    _sessionExpiryTimer?.cancel();
    _countdownTimer?.cancel();
    _retryCountdownTimer?.cancel();
    super.dispose();
  }
}

class ProfileLicenseDetails extends StatefulWidget {
  const ProfileLicenseDetails({super.key, this.canReveal = false});
  final bool canReveal;

  @override
  State<ProfileLicenseDetails> createState() => _ProfileLicenseDetailsState();
}

class _ProfileLicenseDetailsState extends State<ProfileLicenseDetails> {
  bool revealed = false;

  @override
  Widget build(BuildContext context) {
    final controller = AppLicenseScope.of(context);
    if (controller == null || !controller.enabled) {
      return const SizedBox.shrink();
    }
    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) {
        if (!controller.ready) return const SizedBox.shrink();
        final key = controller.licenseKey;
        final showKey = key.isNotEmpty && revealed && widget.canReveal;
        return Padding(
          padding: const EdgeInsets.only(top: 12),
          child: Card(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  LicenseAccountBadge(
                    controller: controller,
                    alignment: WrapAlignment.start,
                  ),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 12,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      SelectableText(
                        key.isEmpty
                            ? '许可证：待联网读取'
                            : '许可证：${showKey ? key : '••••-••••-••••-${key.split('-').last}'}',
                        key: const ValueKey('profile-license-key'),
                      ),
                      if (widget.canReveal && key.isNotEmpty)
                        TextButton(
                          onPressed: () => setState(() => revealed = !revealed),
                          child: Text(showKey ? '隐藏' : '显示完整许可证'),
                        ),
                      if (widget.canReveal && key.isEmpty)
                        TextButton(
                          onPressed: controller.busy
                              ? null
                              : () => controller.perform('licenseVerify'),
                          child: const Text('联网读取许可证'),
                        ),
                    ],
                  ),
                  if (key.isEmpty &&
                      widget.canReveal &&
                      !controller.busy &&
                      controller.message != '授权有效')
                    Text(controller.message),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

class AppLicenseScope extends InheritedWidget {
  const AppLicenseScope({
    super.key,
    required this.controller,
    required super.child,
  });
  final AppLicenseController controller;

  static AppLicenseController? of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppLicenseScope>()?.controller;

  @override
  bool updateShouldNotify(AppLicenseScope oldWidget) =>
      oldWidget.controller != controller;
}

String licenseCountdown(Duration remaining) {
  final seconds = remaining.inSeconds;
  if (seconds <= 0) return '许可证已到期';
  final days = seconds ~/ 86400;
  final hours = (seconds % 86400 ~/ 3600).toString().padLeft(2, '0');
  final minutes = (seconds % 3600 ~/ 60).toString().padLeft(2, '0');
  final rest = (seconds % 60).toString().padLeft(2, '0');
  return '即将到期 $days天 $hours:$minutes:$rest';
}

class LicenseAccountBadge extends StatelessWidget {
  const LicenseAccountBadge({
    super.key,
    this.controller,
    this.alignment = WrapAlignment.end,
  });
  final AppLicenseController? controller;
  final WrapAlignment alignment;

  @override
  Widget build(BuildContext context) {
    final value = controller ?? AppLicenseScope.of(context);
    if (value == null || !value.enabled) return const SizedBox.shrink();
    return AnimatedBuilder(
      animation: value,
      builder: (context, _) {
        if (!value.ready || value.username.isEmpty) {
          return const SizedBox.shrink();
        }
        final expiry = value.expiresAt;
        final remaining = value.remaining;
        final warning = value.expiresSoon && remaining != null;
        final date = expiry?.toLocal().toString().split('.').first;
        final theme = Theme.of(context);
        final text = warning
            ? licenseCountdown(remaining)
            : date == null
            ? '有效期等待复核'
            : '有效至 $date';
        return Tooltip(
          message:
              '专属用户名：${value.username}${date == null ? '' : '\n到期时间：$date'}',
          child: Semantics(
            label: '专属账号 ${value.username}，$text',
            child: Wrap(
              alignment: alignment,
              crossAxisAlignment: WrapCrossAlignment.center,
              spacing: 12,
              runSpacing: 6,
              children: [
                ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 270),
                  child: Text(
                    value.username,
                    key: const ValueKey('licensed-username'),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: theme.textTheme.labelLarge,
                  ),
                ),
                Container(
                  key: ValueKey(
                    warning
                        ? 'license-expiry-countdown'
                        : 'license-valid-until',
                  ),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 7,
                  ),
                  decoration: BoxDecoration(
                    color: warning
                        ? theme.colorScheme.errorContainer
                        : theme.colorScheme.surfaceContainerHighest,
                    borderRadius: BorderRadius.circular(8),
                    border: warning
                        ? Border.all(color: theme.colorScheme.error, width: 1.5)
                        : null,
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        warning
                            ? Icons.timer_outlined
                            : Icons.verified_user_outlined,
                        size: 18,
                        color: warning ? theme.colorScheme.error : null,
                      ),
                      const SizedBox(width: 6),
                      Flexible(
                        child: Text(
                          text,
                          style: theme.textTheme.labelLarge?.copyWith(
                            color: warning
                                ? theme.colorScheme.onErrorContainer
                                : null,
                            fontWeight: warning
                                ? FontWeight.w800
                                : FontWeight.w500,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class LicensedApp extends StatefulWidget {
  const LicensedApp({super.key, required this.child, this.controller});
  final Widget child;
  final AppLicenseController? controller;

  @override
  State<LicensedApp> createState() => _LicensedAppState();
}

class _LicensedAppState extends State<LicensedApp> with WidgetsBindingObserver {
  late final AppLicenseController controller =
      widget.controller ?? AppLicenseController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    unawaited(controller.open());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && !controller.busy) {
      unawaited(controller.perform('licenseStatus'));
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) {
      if (controller.ready) {
        acknowledgeUpdateFrame();
        return AppLicenseScope(controller: controller, child: widget.child);
      }
      if (const {
        'login',
        'activation',
        'verify',
        'renewal',
        'revoked',
      }.contains(controller.phase)) {
        acknowledgeUpdateFrame();
      }
      return MaterialApp(
        key: const ValueKey('license-gate'),
        title: '$appName · 授权',
        debugShowCheckedModeBanner: false,
        theme: AppTheme.light,
        darkTheme: AppTheme.dark,
        home: AppLicenseScreen(controller: controller),
      );
    },
  );
}

class AppLicenseScreen extends StatefulWidget {
  const AppLicenseScreen({super.key, required this.controller});
  final AppLicenseController controller;

  @override
  State<AppLicenseScreen> createState() => _AppLicenseScreenState();
}

class _AppLicenseScreenState extends State<AppLicenseScreen> {
  final username = TextEditingController();
  final password = TextEditingController();
  final licenseKey = TextEditingController();
  bool licenseKeyVisible = false;
  bool checkingConnection = false;
  String connectionMessage = '';

  Future<void> checkConnection() async {
    if (checkingConnection || widget.controller.busy) return;
    setState(() {
      checkingConnection = true;
      connectionMessage = '';
    });
    try {
      final result = await widget.controller.checkNetwork();
      if (mounted) {
        setState(
          () => connectionMessage = result['message'] as String? ?? '检测完成',
        );
      }
    } on AppFailure catch (error) {
      if (mounted) setState(() => connectionMessage = error.message);
    } catch (_) {
      if (mounted) setState(() => connectionMessage = '连接检测未完成，请检查本机网络设置。');
    } finally {
      if (mounted) setState(() => checkingConnection = false);
    }
  }

  @override
  void dispose() {
    username.dispose();
    password.dispose();
    licenseKey.dispose();
    super.dispose();
  }

  Future<void> login() async {
    if (widget.controller.busy ||
        checkingConnection ||
        widget.controller.requestCoolingDown) {
      return;
    }
    final value = password.text;
    password.clear();
    await widget.controller.perform(
      'licenseLogin',
      username: username.text.trim(),
      password: value,
    );
  }

  Future<void> activateLicense() async {
    if (widget.controller.busy ||
        checkingConnection ||
        widget.controller.requestCoolingDown) {
      return;
    }
    final value = licenseKey.text.trim();
    setState(() => licenseKeyVisible = false);
    await widget.controller.perform('licenseActivate', key: value);
    if (mounted && widget.controller.ready) licenseKey.clear();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.controller,
    builder: (context, _) => _buildGate(context),
  );

  Widget _buildGate(BuildContext context) {
    final controller = widget.controller;
    final submitDisabled =
        controller.busy || checkingConnection || controller.requestCoolingDown;
    final loginRequired = controller.phase == 'login';
    final revoked = controller.phase == 'revoked';
    final activationRequired = controller.phase == 'activation' || revoked;
    return Scaffold(
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(32),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 500),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(28),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    const Icon(Icons.verified_user_outlined, size: 52),
                    const SizedBox(height: 16),
                    Text(
                      '$appName · 登录授权',
                      textAlign: TextAlign.center,
                      style: Theme.of(context).textTheme.headlineSmall,
                    ),
                    const SizedBox(height: 24),
                    Text(
                      controller.message,
                      key: const ValueKey('license-message'),
                    ),
                    if (controller.message.contains('[AUTH_')) ...[
                      Align(
                        alignment: Alignment.centerLeft,
                        child: TextButton(
                          onPressed:
                              checkingConnection ||
                                  controller.busy ||
                                  controller.requestCoolingDown
                              ? null
                              : checkConnection,
                          child: Text(
                            checkingConnection ? '正在检测连接…' : '检测授权连接',
                          ),
                        ),
                      ),
                      if (connectionMessage.isNotEmpty) Text(connectionMessage),
                    ],
                    if (controller.requestCoolingDown) ...[
                      const SizedBox(height: 12),
                      Text(
                        '请在 ${controller.retryCountdown}后再次提交。等待期间不会重复发送请求。',
                        key: const ValueKey('license-retry-countdown'),
                        style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          fontWeight: FontWeight.w700,
                          color: Theme.of(context).colorScheme.primary,
                        ),
                      ),
                    ],
                    if (controller.username.isNotEmpty) ...[
                      const SizedBox(height: 12),
                      Text('专属用户名：${controller.username}'),
                      if (controller.expiresAt != null)
                        Text(
                          '许可证到期：${controller.expiresAt!.toLocal().toString().split('.').first}',
                        ),
                    ],
                    if (loginRequired) ...[
                      const SizedBox(height: 20),
                      TextField(
                        controller: username,
                        enabled: !controller.busy,
                        autocorrect: false,
                        enableSuggestions: false,
                        keyboardType: TextInputType.emailAddress,
                        decoration: const InputDecoration(
                          labelText: '专属用户名',
                          hintText: '专属邮箱用户名',
                        ),
                      ),
                      const SizedBox(height: 12),
                      TextField(
                        controller: password,
                        enabled: !controller.busy,
                        obscureText: true,
                        autocorrect: false,
                        enableSuggestions: false,
                        onSubmitted: (_) {
                          if (!submitDisabled) unawaited(login());
                        },
                        decoration: const InputDecoration(labelText: '8 位专属密码'),
                      ),
                      const SizedBox(height: 20),
                      FilledButton(
                        onPressed: submitDisabled ? null : login,
                        child: const Text('登录'),
                      ),
                    ] else if (activationRequired) ...[
                      const SizedBox(height: 20),
                      TextField(
                        controller: licenseKey,
                        enabled: !controller.busy,
                        obscureText: !licenseKeyVisible,
                        autocorrect: false,
                        enableSuggestions: false,
                        decoration: InputDecoration(
                          labelText: '许可证 key',
                          suffixIcon: IconButton(
                            key: const ValueKey('license-key-visibility'),
                            tooltip: licenseKeyVisible ? '隐藏许可证' : '显示许可证',
                            onPressed: controller.busy
                                ? null
                                : () => setState(
                                    () =>
                                        licenseKeyVisible = !licenseKeyVisible,
                                  ),
                            icon: Icon(
                              licenseKeyVisible
                                  ? Icons.visibility_off_outlined
                                  : Icons.visibility_outlined,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(height: 20),
                      FilledButton(
                        onPressed: submitDisabled ? null : activateLicense,
                        child: Text(
                          controller.requestCoolingDown
                              ? '${controller.retryCountdown}后可重新激活'
                              : '联网激活当前电脑',
                        ),
                      ),
                      if (revoked)
                        TextButton(
                          onPressed: submitDisabled
                              ? null
                              : () => controller.perform('licenseVerify'),
                          child: const Text('联网复核许可证'),
                        ),
                    ] else if (controller.phase == 'verify' ||
                        controller.phase == 'renewal') ...[
                      const SizedBox(height: 20),
                      FilledButton(
                        onPressed: submitDisabled
                            ? null
                            : () => controller.perform('licenseVerify'),
                        child: Text(
                          controller.phase == 'renewal' ? '续期后联网复核' : '联网复核许可证',
                        ),
                      ),
                    ] else if (controller.phase != 'locked' &&
                        controller.phase != 'loading') ...[
                      const SizedBox(height: 20),
                      OutlinedButton(
                        onPressed: controller.busy
                            ? null
                            : () => controller.perform('licenseStatus'),
                        child: const Text('重新检查'),
                      ),
                    ],
                    if (controller.busy)
                      const Padding(
                        padding: EdgeInsets.only(top: 20),
                        child: LinearProgressIndicator(),
                      ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
