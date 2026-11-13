package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const hongguoPlaybackRegisterURL = "https://log5-applog-lf.fqnovel.com/service/2/device_register/?tt_data=a&cronet_version=8d40f833_2026-03-03&ttnet_version=4.2.243.31-douyin&use_store_region_cookie=1"
const hongguoRegisteredPlaybackUserAgent = hongguoAppUserAgent

type hongguoPlaybackDevice struct {
	Version   int                     `json:"version"`
	DeviceID  string                  `json:"deviceId"`
	InstallID string                  `json:"installId"`
	ClientID  string                  `json:"clientId,omitempty"`
	Cookies   []hongguoPlaybackCookie `json:"cookies,omitempty"`
}

// valid 判断红果播放设备身份的字段与约束是否有效。
func (device hongguoPlaybackDevice) valid() bool {
	if len(device.Cookies) > 8 {
		return false
	}
	for _, cookie := range device.Cookies {
		if !cookie.valid() {
			return false
		}
	}
	return device.Version == 1 && hongguoNumericID.MatchString(device.DeviceID) && strings.Trim(device.DeviceID, "0") != "" &&
		hongguoNumericID.MatchString(device.InstallID) && strings.Trim(device.InstallID, "0") != "" &&
		(device.ClientID == "" || len(device.ClientID) == 36 && !strings.ContainsAny(device.ClientID, "\x00\r\n/?&#"))
}

type hongguoPlaybackResponseError struct {
	message string
}

// Error 返回当前错误的可显示文本。
func (failure *hongguoPlaybackResponseError) Error() string { return failure.message }

// hongguoPlaybackDevice 取得当前保存的红果播放设备身份。
func (d *Downloader) hongguoPlaybackDevice(ctx context.Context) (hongguoPlaybackDevice, error) {
	client := d.hongguoClient()
	client.playbackDeviceMu.Lock()
	defer client.playbackDeviceMu.Unlock()
	if client.playbackDevice != nil {
		return cloneHongguoPlaybackDevice(*client.playbackDevice), nil
	}
	if client.playbackDeviceErr != nil && time.Now().Before(client.playbackDeviceRetryAt) {
		return hongguoPlaybackDevice{}, client.playbackDeviceErr
	}
	path := filepath.Join(d.cfg.dataDir, "hongguo-playback-device.json")
	var device hongguoPlaybackDevice
	if body, err := os.ReadFile(path); err == nil {
		if len(body) > 24<<10 || json.Unmarshal(body, &device) != nil || !device.valid() {
			return device, &hongguoPlaybackResponseError{"红果已保存的播放设备信息无效，原文件保留，请检查应用数据目录"}
		}
	} else if !os.IsNotExist(err) {
		return device, fmt.Errorf("无法读取红果播放设备信息：%w", err)
	} else {
		client.mu.Lock()
		if client.state.Registered {
			device = hongguoPlaybackDevice{Version: 1, DeviceID: client.state.DeviceID, InstallID: client.state.InstallID}
		}
		client.mu.Unlock()
		if !device.valid() {
			device, err = d.registerHongguoPlaybackDevice(ctx)
			if err != nil {
				if ctx.Err() == nil {
					client.playbackDeviceErr = err
					client.playbackDeviceRetryAt = time.Now().Add(time.Minute)
				}
				return device, err
			}
		}
		body, err = json.Marshal(device)
		if err != nil {
			return device, err
		}
		if err = nativeDownloadWrite(path, body); err != nil {
			return device, fmt.Errorf("无法保存红果播放设备信息，未使用临时设备号：%w", err)
		}
	}
	client.playbackDevice = &device
	return cloneHongguoPlaybackDevice(device), nil
}

// hongguoPlaybackRegistrationBody 构造播放设备注册请求正文。
func hongguoPlaybackRegistrationBody(now time.Time, clientID string) ([]byte, error) {
	header := map[string]any{
		"display_name": "红果免费短剧", "update_version_code": hongguoPlaybackVersionCode, "manifest_version_code": hongguoPlaybackVersionCode,
		"app_version_minor": hongguoPlaybackVersionName, "aid": 8662, "channel": "update_64", "package": "com.phoenix.read",
		"app_version": hongguoPlaybackVersionName, "version_code": hongguoPlaybackVersionCode, "sdk_version": "3.7.0-rc.34-fanqie-xiaoshuo",
		"sdk_target_version": 29, "git_hash": "3a88f4e", "os": "Android", "os_version": "16", "os_api": 36,
		"device_model": "25053RT47C", "device_brand": "Redmi", "device_manufacturer": "Redmi", "cpu_abi": "arm64-v8a",
		"release_build": "b5e187f", "density_dpi": 520, "display_density": "mdpi", "resolution": "1280x2772",
		"language": "zh", "timezone": 8, "access": "wifi", "not_request_sender": 0, "carrier": "CHINA MOBILE",
		"mcc_mnc": "46000", "rom": "eng.root.20231116.160340", "rom_version": "W528JS release-keys", "cdid": clientID,
		"sig_hash": "56a962410c494bbaf0b58dba20cae56f", "openudid": randomHex(8), "clientudid": uuidLike(),
		"ipv6_list": []string{}, "region": "CN", "tz_name": "Asia/Shanghai", "tz_offset": 28800, "sim_region": "cn",
		"sim_serial_number": []string{}, "oaid_may_support": false, "req_id": uuidLike(), "device_platform": "android",
		"custom":                map[string]any{"account_region_src": "fallback", "account_region": "cn", "host_bit": 64, "dragon_device_type": "phone"},
		"pre_installed_channel": "", "apk_first_install_time": now.UnixMilli(), "is_system_app": 0, "sdk_flavor": "china", "guest_mode": 0,
	}
	plain, err := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": now.UnixMilli()})
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err = writer.Write(plain); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	seed := make([]byte, 32)
	if _, err = rand.Read(seed); err != nil {
		return nil, err
	}
	first := sha512.Sum512(seed)
	salt := mustHex("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")
	derived := sha512.Sum512(append(first[:], salt...))
	hash := sha512.Sum512(compressed.Bytes())
	payload := append(hash[:], compressed.Bytes()...)
	padding := aes.BlockSize - len(payload)%aes.BlockSize
	payload = append(payload, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(derived[:16])
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, len(payload))
	cipher.NewCBCEncrypter(block, derived[16:32]).CryptBlocks(encoded, payload)
	return append(append(mustHex("746305100000"), seed...), encoded...), nil
}

// registerHongguoPlaybackDevice 注册播放请求所需的设备身份。
func (d *Downloader) registerHongguoPlaybackDevice(ctx context.Context) (hongguoPlaybackDevice, error) {
	clientID, now := uuidLike(), time.Now()
	body, err := hongguoPlaybackRegistrationBody(now, clientID)
	if err != nil {
		return hongguoPlaybackDevice{}, err
	}
	stub := md5.Sum(body)
	headers := map[string]string{
		"User-Agent": hongguoPlaybackRegistrationUserAgent, "Accept-Encoding": "identity",
		"Accept": "application/json", "Content-Type": "application/octet-stream;tt-data=a", "log-encode-type": "gzip",
		"x-ss-dp": "8662", "x-ss-req-ticket": strconv.FormatInt(now.UnixMilli(), 10), "x-vc-bdturing-sdk-version": "4.0.3.cn",
		"lc": "101", "sdk-version": "2", "passport-sdk-version": "5051452", "x-neptune": "-8|50:51:59:30:40:47:49:39",
		"x-ss-stub": strings.ToUpper(hex.EncodeToString(stub[:])),
	}
	var cookies []hongguoPlaybackCookie
	ctx = hongguoPlaybackResponseContext(ctx, func(request *http.Request, response *http.Response) error {
		cookies = hongguoMergePlaybackCookies(nil, request, response, now)
		return nil
	})
	response, err := d.hongguoMultiFetchJSON(ctx, hongguoPlaybackRegisterURL, http.MethodPost, headers, body, nil)
	if err != nil {
		return hongguoPlaybackDevice{}, err
	}
	if code := firstNonEmpty(mapString(response, "code", "Code"), "0"); code != "0" {
		return hongguoPlaybackDevice{}, &hongguoPlaybackResponseError{"红果播放设备注册未获接受，请确认源站合法访问条件"}
	}
	device := hongguoPlaybackDevice{Version: 1, DeviceID: mapString(response, "device_id_str", "device_id"), InstallID: mapString(response, "install_id_str", "install_id"), ClientID: clientID, Cookies: cookies}
	if !device.valid() {
		return device, &hongguoPlaybackResponseError{"红果播放设备注册未返回有效身份，未自动轮换或使用临时设备号"}
	}
	return device, nil
}

// hongguoStopPlaybackFallback 判断红果播放错误是否要求停止后续回退。
func hongguoStopPlaybackFallback(err error) bool {
	var response *hongguoPlaybackResponseError
	var backoff *requestBackoff
	return errors.As(err, &response) || errors.As(err, &backoff)
}
