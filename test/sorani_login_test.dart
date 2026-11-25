import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/sorani_login_screen.dart';

void main() {
  test('Sorani login only accepts the exact original HTTPS origin', () {
    expect(soraniLoginOrigin(Uri.parse('https://www.sorani.net/')), isTrue);
    for (final value in [
      'http://www.sorani.net/',
      'https://www.sorani.net.foreign.example/',
      'https://foreign.example/',
      'https://name@www.sorani.net/',
      'https://www.sorani.net:8443/',
    ]) {
      expect(soraniLoginOrigin(Uri.parse(value)), isFalse);
    }
  });

  test('Sorani login requirement and expiry offer account recovery', () {
    expect(soraniLoginRequired('青空需要登录，请先自行注册账号'), isTrue);
    expect(soraniLoginRequired('青空登录已过期，请重新登录'), isTrue);
    expect(soraniLoginRequired('青空暂无可用播放地址'), isFalse);
    expect(soraniLoginRequired('其他站源需要登录'), isFalse);
  });

  test('Anonymous Sorani users do not see stale generic playback errors', () {
    expect(
      soraniSourceError(
        '青空次元该分集暂无可用播放地址（线路 anime_jp_m3u8）',
        authenticated: false,
      ),
      contains('青空需要登录'),
    );
    expect(soraniSourceError('青空连接超时', authenticated: false), '青空连接超时');
  });
}
