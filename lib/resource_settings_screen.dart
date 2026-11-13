import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'core_bridge.dart';
import 'download_preferences.dart';
import 'local_store.dart';
import 'resource_settings.dart';
import 'widgets.dart';
import 'cache_settings.dart';

class DownloadPreferencesScreen extends StatelessWidget {
  const DownloadPreferencesScreen({super.key, required this.store});
  final LocalStore store;
  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: store,
    builder: (context, _) {
      final preferences = store.downloadPreferences;
      return Scaffold(
        appBar: AppBar(title: const Text('下载偏好')),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 680),
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                ListTile(
                  title: const Text('默认画质'),
                  subtitle: const Text('指定画质不可用时，使用源站提供的可用版本。'),
                ),
                DropdownButtonFormField<int>(
                  initialValue: preferences.quality,
                  decoration: const InputDecoration(labelText: '下载画质'),
                  items: [
                    for (final value in DownloadPreferences.qualities)
                      DropdownMenuItem(
                        value: value,
                        child: Text(value == 0 ? '自动 · 优先高清' : '${value}P'),
                      ),
                  ],
                  onChanged: !store.canDownload
                      ? null
                      : (value) {
                          if (value != null) {
                            saveUserChange(
                              context,
                              () => store.setDownloadPreferences(
                                preferences.copyWith(quality: value),
                              ),
                            );
                          }
                        },
                ),
                const SizedBox(height: 12),
                SwitchListTile(
                  value: preferences.includeVip,
                  title: const Text('默认包含 VIP 集'),
                  subtitle: const Text('VIP 集可能仅提供试看内容；下载前仍可调整选择。'),
                  onChanged: !store.canDownload
                      ? null
                      : (value) => saveUserChange(
                          context,
                          () => store.setDownloadPreferences(
                            preferences.copyWith(includeVip: value),
                          ),
                        ),
                ),
                const Padding(
                  padding: EdgeInsets.all(16),
                  child: Text('偏好保存在当前用户中，应用于下载选集、批量下载和更新本剧。'),
                ),
              ],
            ),
          ),
        ),
      );
    },
  );
}

class ResourceSettingsScreen extends StatefulWidget {
  const ResourceSettingsScreen({
    super.key,
    required this.repository,
    required this.store,
    this.logDirectoryPicker,
  });
  final AppRepository repository;
  final LocalStore store;
  final Future<String?> Function()? logDirectoryPicker;
  @override
  State<ResourceSettingsScreen> createState() => _ResourceSettingsScreenState();
}

class _ResourceSettingsScreenState extends State<ResourceSettingsScreen> {
  final _form = GlobalKey<FormState>();
  final _cacheEdits = {
    for (final spec in cacheSpecs)
      spec.key: TextEditingController(text: '${spec.defaultValue}'),
  };
  Map<String, CacheUsage> _cacheUsage = {};
  bool _refreshing = false;
  DateTime? _usageAt;
  final _proxy = TextEditingController();
  final _logDirectory = TextEditingController();
  bool _choosingLogDirectory = false;
  late final int _epoch = widget.store.profileEpoch;
  ResourceSettings? _settings;
  String _mode = 'auto';
  int _catalog = 3, _interval = 250, _downloads = 2, _sourceDownloads = 1;
  bool _folders = false, _busy = false, _showProxy = false;
  String? _error;
  bool get _allowed =>
      widget.store.profileEpoch == _epoch &&
      !widget.store.locked &&
      widget.store.profile.admin;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _proxy.dispose();
    _logDirectory.dispose();
    for (final controller in _cacheEdits.values) {
      controller.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final settings = await widget.repository.resourceSettings();
      if (!mounted || !_allowed) return;
      setState(() {
        _settings = settings;
        _mode = settings.proxyMode;
        _proxy.text = settings.proxyUrl;
        _logDirectory.text = settings.logDirectory;
        _catalog = settings.catalogConcurrency;
        _interval = settings.catalogIntervalMs;
        _downloads = settings.downloadConcurrency;
        _sourceDownloads = settings.downloadSourceConcurrency;
        _folders = settings.downloadBySource;
        for (final spec in cacheSpecs) {
          _cacheEdits[spec.key]!.text =
              '${settings.cacheLimits.value(spec.key)}';
        }
        _error = null;
      });
      CacheRuntime.apply(settings.cacheLimits);
      await _refreshUsage(snapshot: settings);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _save() async {
    if (_busy || !_allowed) return;
    for (final spec in cacheSpecs) {
      final value = int.tryParse(_cacheEdits[spec.key]!.text);
      if (value == null || value < spec.minimum || value > spec.maximum) {
        _form.currentState?.validate();
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '${spec.title}：请输入 ${spec.minimum}–${spec.maximum} ${spec.unit}',
            ),
          ),
        );
        return;
      }
    }
    if (!(_form.currentState?.validate() ?? false)) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings = await widget.repository.saveResourceSettings(
        ResourceSettings(
          logDirectory: _logDirectory.text.trim(),
          proxyMode: _mode,
          proxyUrl: _proxy.text.trim(),
          catalogConcurrency: _catalog,
          catalogIntervalMs: _interval,
          downloadConcurrency: _downloads,
          downloadSourceConcurrency: _sourceDownloads,
          downloadBySource: _folders,
          cacheLimits: CacheLimits({
            for (final spec in cacheSpecs)
              spec.key: int.parse(_cacheEdits[spec.key]!.text),
          }),
        ),
      );
      if (!mounted || !_allowed) return;
      setState(() => _settings = settings);
      CacheRuntime.apply(settings.cacheLimits);
      await _refreshUsage(
        snapshot: settings.cacheUsage.isEmpty ? null : settings,
      );
      if (!mounted || !_allowed) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('设置已保存；缓存生效时机见各项说明，不中断当前播放')),
      );
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _refreshUsage({ResourceSettings? snapshot}) async {
    if (_refreshing || !_allowed) return;
    setState(() => _refreshing = true);
    try {
      final settings = snapshot ?? await widget.repository.resourceSettings();
      final frontend = await CacheRuntime.sampleUsage();
      if (!mounted || !_allowed) return;
      setState(() {
        _settings = settings;
        _cacheUsage = {...settings.cacheUsage, ...frontend};
        _usageAt = DateTime.now();
      });
    } catch (error) {
      if (mounted && _allowed) {
        setState(() => _error = '缓存占用刷新失败：$error');
      }
    } finally {
      if (mounted) setState(() => _refreshing = false);
    }
  }

  Future<void> _chooseLogDirectory() async {
    if (_busy || _choosingLogDirectory || !_allowed) return;
    setState(() => _choosingLogDirectory = true);
    try {
      final directory =
          await (widget.logDirectoryPicker?.call() ??
              FilePicker.getDirectoryPath(dialogTitle: '选择日志保存目录'));
      if (mounted && _allowed && directory != null) {
        setState(() => _logDirectory.text = directory);
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _choosingLogDirectory = false);
    }
  }

  Widget _cacheControl(CacheSpec spec) {
    final usage = _cacheUsage[spec.key] ?? const CacheUsage();
    final saved = _settings!.cacheLimits.value(spec.key);
    return Card(
      key: ValueKey('cache-${spec.key}'),
      margin: const EdgeInsets.symmetric(vertical: 6),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(spec.title, style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            Text('当前占用：${usage.label(spec)} · 已保存上限：$saved ${spec.unit}'),
            if (usage.detail.isNotEmpty) Text(usage.detail),
            const SizedBox(height: 12),
            TextFormField(
              key: ValueKey('cache-limit-${spec.key}'),
              controller: _cacheEdits[spec.key],
              enabled: !_busy,
              keyboardType: TextInputType.number,
              inputFormatters: [FilteringTextInputFormatter.digitsOnly],
              decoration: InputDecoration(
                labelText: '新上限（${spec.unit}）',
                helperText:
                    '最小 ${spec.minimum} · 默认 ${spec.defaultValue} · 最大 ${spec.maximum}\n${spec.scope}',
                helperMaxLines: 4,
              ),
              validator: (value) {
                final number = int.tryParse(value ?? '');
                return number == null ||
                        number < spec.minimum ||
                        number > spec.maximum
                    ? '请输入 ${spec.minimum}–${spec.maximum} 之间的整数'
                    : null;
              },
            ),
          ],
        ),
      ),
    );
  }

  Widget _count(
    String title,
    int value,
    ValueChanged<int> change, {
    int maximum = 6,
  }) => DropdownButtonFormField<int>(
    initialValue: value,
    decoration: InputDecoration(labelText: title),
    items: [
      for (var i = 1; i <= maximum; i++)
        DropdownMenuItem(value: i, child: Text('$i')),
    ],
    onChanged: _busy
        ? null
        : (value) {
            if (value != null) setState(() => change(value));
          },
  );

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('网络与资源'),
      actions: [
        IconButton(
          tooltip: '刷新缓存占用',
          onPressed: _busy || _refreshing || _settings == null || !_allowed
              ? null
              : () => _refreshUsage(),
          icon: const Icon(Icons.refresh),
        ),
        TextButton(
          onPressed: _busy || _settings == null || !_allowed ? null : _save,
          child: const Text('保存'),
        ),
      ],
    ),
    body: !_allowed
        ? const StatusPanel(title: '需要管理员权限', message: '请返回后切换管理员。')
        : _settings == null
        ? _error == null
              ? const Center(child: CircularProgressIndicator())
              : StatusPanel(title: '无法读取设置', message: _error!, onRetry: _load)
        : Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 720),
              child: Form(
                key: _form,
                child: ListView(
                  padding: const EdgeInsets.all(20),
                  children: [
                    if (_busy) const LinearProgressIndicator(),
                    if (Platform.isWindows) ...[
                      Text(
                        '日志保存位置',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const Text(
                        '默认在软件目录下的 logs 中，下载默认在软件目录下的 download 中；用户指定的目录优先。切换日志目录不移动或删除旧日志。',
                      ),
                      const SizedBox(height: 8),
                      if (_settings!.logPath.isNotEmpty)
                        SelectableText('当前日志文件：${_settings!.logPath}'),
                      if (_settings!.defaultLogDirectory.isNotEmpty)
                        Text('默认日志目录：${_settings!.defaultLogDirectory}'),
                      TextFormField(
                        key: const ValueKey('resource-log-directory'),
                        controller: _logDirectory,
                        enabled: !_busy && !_choosingLogDirectory,
                        decoration: const InputDecoration(
                          labelText: '自定义日志目录',
                          hintText: '留空使用软件目录下的 logs',
                          helperText: '选择或输入绝对路径，点击顶部“保存”后生效',
                        ),
                      ),
                      Wrap(
                        children: [
                          TextButton.icon(
                            onPressed: _busy || _choosingLogDirectory
                                ? null
                                : _chooseLogDirectory,
                            icon: const Icon(Icons.folder_open_outlined),
                            label: const Text('选择日志目录'),
                          ),
                          TextButton(
                            onPressed: _busy || _choosingLogDirectory
                                ? null
                                : () => setState(_logDirectory.clear),
                            child: const Text('使用软件默认目录'),
                          ),
                        ],
                      ),
                      const SizedBox(height: 24),
                    ],
                    if (_settings!.warning.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 16),
                        child: Text(_settings!.warning),
                      ),
                    DropdownButtonFormField<String>(
                      initialValue: _mode,
                      decoration: const InputDecoration(labelText: '连接方式'),
                      items: const [
                        DropdownMenuItem(value: 'auto', child: Text('自动')),
                        DropdownMenuItem(value: 'direct', child: Text('直连')),
                        DropdownMenuItem(value: 'manual', child: Text('手动代理')),
                      ],
                      onChanged: _busy
                          ? null
                          : (value) => setState(() => _mode = value ?? 'auto'),
                    ),
                    if (_mode == 'auto')
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 12),
                        child: Text(
                          '${_settings!.systemProxyStatus.isEmpty ? '使用系统静态代理；未配置时使用环境变量或直连。' : _settings!.systemProxyStatus}\n'
                          '仅代理连接中断或超时时，同一地址直连重试一次；'
                          'HTTP 错误、验证码和付费限制不切换。'
                          '如需始终通过代理，请选择手动代理。',
                        ),
                      ),
                    if (_mode == 'manual')
                      Padding(
                        padding: const EdgeInsets.only(top: 16),
                        child: TextField(
                          controller: _proxy,
                          obscureText: !_showProxy,
                          enableSuggestions: false,
                          autocorrect: false,
                          decoration: InputDecoration(
                            labelText: '代理地址',
                            hintText: 'http://127.0.0.1:7890',
                            helperText: '支持 HTTP、HTTPS、SOCKS5；仅保存在本机，不进入配置备份。',
                            helperMaxLines: 3,
                            suffixIcon: IconButton(
                              tooltip: _showProxy ? '隐藏代理地址' : '显示代理地址',
                              onPressed: () =>
                                  setState(() => _showProxy = !_showProxy),
                              icon: Icon(
                                _showProxy
                                    ? Icons.visibility_off_outlined
                                    : Icons.visibility_outlined,
                              ),
                            ),
                          ),
                        ),
                      ),
                    const SizedBox(height: 28),
                    _count('目录请求并发', _catalog, (value) => _catalog = value),
                    const SizedBox(height: 16),
                    Text('目录请求间隔 · $_interval 毫秒'),
                    Slider(
                      value: _interval.toDouble(),
                      min: 0,
                      max: 5000,
                      divisions: 100,
                      label: '$_interval 毫秒',
                      onChanged: _busy
                          ? null
                          : (value) =>
                                setState(() => _interval = value.round()),
                    ),
                    const SizedBox(height: 16),
                    _count(
                      '同时下载总数量 · 1–10 集',
                      _downloads,
                      (value) => _downloads = value,
                      maximum: 10,
                    ),
                    const SizedBox(height: 16),
                    _count(
                      '同站点同时下载 · 1–10 集',
                      _sourceDownloads,
                      (value) => _sourceDownloads = value,
                      maximum: 10,
                    ),
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: 12),
                      child: Text(
                        '同站点默认逐集排队，实际并发不超过总数量；每集启动及上一集结束后随机间隔 100–500ms。调低上限不会中断正在下载的分集。遇到 403、429 或 Retry-After 时按站点要求退避，不绕过验证，也不能保证 IP 不被限制。',
                      ),
                    ),
                    const SizedBox(height: 12),
                    SwitchListTile(
                      contentPadding: EdgeInsets.zero,
                      value: _folders,
                      title: const Text('按站源分类保存'),
                      subtitle: const Text(
                        '仅临时下载缓存按站源分组；MP4 成品仍按剧名平铺，已有下载继续使用原位置。',
                      ),
                      onChanged: _busy
                          ? null
                          : (value) => setState(() => _folders = value),
                    ),
                    const Divider(height: 32),
                    Text('缓存容量', style: Theme.of(context).textTheme.titleLarge),
                    const SizedBox(height: 8),
                    const Text(
                      '占用为刷新时的快照，未知值不会显示成零。默认为翻倍后的容量，最小为翻倍前，最大为最小值的8倍。调低上限可能淘汰旧海报和榜单缓存，不删除剧库、健康历史或视频；剧库/站源记录占用超过拟设上限时拒绝保存。较大的内存缓冲可能增加内存压力，并不保证不会崩溃。',
                    ),
                    if (_refreshing) const LinearProgressIndicator(),
                    if (_usageAt != null)
                      Text(
                        '占用更新时间：${_usageAt!.toLocal().toString().split('.').first}',
                      ),
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TextButton(
                        onPressed: _busy
                            ? null
                            : () => setState(() {
                                for (final spec in cacheSpecs) {
                                  _cacheEdits[spec.key]!.text =
                                      '${spec.defaultValue}';
                                }
                              }),
                        child: const Text('恢复缓存默认值（保存后生效）'),
                      ),
                    ),
                    for (final spec in cacheSpecs) _cacheControl(spec),
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 12),
                        child: Text(
                          _error!,
                          style: TextStyle(
                            color: Theme.of(context).colorScheme.error,
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
  );
}
