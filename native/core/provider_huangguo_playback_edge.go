package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/hkdf"
)

const (
	huangguoPlaybackProtocol    = "dx150-edge-v1"
	huangguoPlaybackScope       = "playback"
	huangguoPlaybackRequestPath = "/api/playback/request"
	huangguoPlaybackMaxSequence = 16384
)

var errHuangguoPlaybackSessionExpired = errors.New("huangguo playback session expired")

type huangguoPlaybackReadKey struct{}

type huangguoPlaybackPageReadKey struct{}

type huangguoPlaybackEdgeSession struct {
	mu          sync.Mutex
	info        string
	kid         string
	sid         string
	requestKey  []byte
	responseKey []byte
	clockOffset int64
	expiresAt   int64
	sequence    uint32
}

type huangguoPlaybackHello struct {
	Version    int    `json:"v"`
	Type       string `json:"type"`
	ClientPub  string `json:"client_pub,omitempty"`
	Kid        string `json:"kid,omitempty"`
	SID        string `json:"sid,omitempty"`
	Site       string `json:"site,omitempty"`
	ServerTime int64  `json:"server_time,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	ServerPub  string `json:"server_pub,omitempty"`
	Salt       string `json:"salt,omitempty"`
}

type huangguoPlaybackCall struct {
	Version    int    `json:"v"`
	Type       string `json:"type"`
	Kid        string `json:"kid"`
	SID        string `json:"sid"`
	Sequence   uint32 `json:"seq"`
	Timestamp  int64  `json:"ts"`
	Ciphertext string `json:"ciphertext"`
}

type huangguoPlaybackCallResult struct {
	Version    int    `json:"v"`
	Type       string `json:"type"`
	Kid        string `json:"kid"`
	SID        string `json:"sid"`
	Sequence   uint32 `json:"seq"`
	Timestamp  int64  `json:"ts"`
	Ciphertext string `json:"ciphertext"`
}

type huangguoPlaybackBusinessRequest struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

type huangguoPlaybackBusinessPacket struct {
	Request huangguoPlaybackBusinessRequest `json:"request"`
}

type huangguoPlaybackInnerResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type huangguoPlaybackEnvelope struct {
	Status  int                      `json:"status"`
	Message string                   `json:"msg"`
	Data    huangguoPlaybackResponse `json:"data"`
}

type huangguoPlaybackResponse struct {
	ID             json.RawMessage      `json:"id"`
	Episode        int                  `json:"ep"`
	ManifestMethod string               `json:"manifest_method"`
	Streams        huangguoPlaybackURLs `json:"streams"`
	CDN            huangguoPlaybackCDN  `json:"cdn"`
}

type huangguoPlaybackURLs struct {
	Main string `json:"main"`
	HEVC string `json:"hevc"`
}

type huangguoPlaybackCDN struct {
	M3U8Enabled  bool   `json:"m3u8Enabled"`
	OtherEnabled bool   `json:"otherEnabled"`
	AuthKey      string `json:"authKey"`
	AuthValue    string `json:"authValue"`
	App          string `json:"app"`
}

func (d *Downloader) resolveHuangguoAIPlayback(ctx context.Context, pageURL string, chapter Chapter, fallback int) (providerMedia, error) {
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return providerMedia{}, errors.New("黄果 AI 分集页面地址无效")
	}
	kind, videoID, valid := huangguoWorkRoute(pageURL)
	if !valid || kind != "video" || !validPositiveDecimal(videoID, 19) {
		return providerMedia{}, errors.New("黄果 AI 分集页面缺少有效作品编号")
	}
	episode, err := strconv.Atoi(chapter.EpisodeString(fallback))
	if err != nil || episode < 1 || episode > 2147483647 {
		return providerMedia{}, errors.New("黄果 AI 分集编号无效")
	}
	if match := huangguoEpisodePath.FindStringSubmatch(parsed.Path); len(match) == 3 {
		explicit, parseErr := strconv.Atoi(match[2])
		if parseErr != nil || explicit != episode {
			return providerMedia{}, errors.New("黄果 AI 分集页面与章节编号不一致")
		}
	} else if episode != 1 || strings.TrimRight(parsed.Path, "/") != "/video/"+videoID {
		return providerMedia{}, errors.New("黄果 AI 分集页面格式无效")
	}
	origin := providerMediaOrigin(parsed)
	session := d.huangguoPlaybackSession(origin)
	payload, err := session.resolve(ctx, d, origin, pageURL, videoID, episode)
	if err != nil {
		return providerMedia{}, err
	}
	identity := strings.Trim(strings.TrimSpace(string(payload.ID)), `"`)
	if identity != videoID || payload.Episode != episode || payload.ManifestMethod != http.MethodGet {
		return providerMedia{}, errors.New("黄果 AI 播放接口返回的作品或分集不一致")
	}
	mediaURL := strings.TrimSpace(payload.Streams.Main)
	mediaAddress, err := url.Parse(mediaURL)
	if err != nil || mediaAddress.User != nil || !isProviderHTTPMediaURL(mediaURL) {
		return providerMedia{}, errors.New("黄果 AI 播放接口未返回有效清单地址")
	}
	credentials := &providerMediaCredentials{
		origin:       providerMediaOrigin(mediaAddress),
		referer:      pageURL,
		userAgent:    userAgent,
		expires:      huangguoPlaybackURLExpiry(mediaAddress),
		cdnAuthKey:   validHuangguoCDNHeader(payload.CDN.AuthKey, payload.CDN.AuthValue),
		cdnAuthValue: payload.CDN.AuthValue,
		cdnApp:       payload.CDN.App,
		cdnM3U8:      payload.CDN.M3U8Enabled,
		cdnOther:     payload.CDN.OtherEnabled,
	}
	if credentials.cdnAuthKey == "" || len(credentials.cdnApp) > 128 {
		credentials.cdnAuthKey, credentials.cdnAuthValue, credentials.cdnApp = "", "", ""
	}
	return providerMedia{URL: mediaURL, Referer: pageURL, credentials: credentials}, nil
}

func (d *Downloader) huangguoPlaybackSession(origin string) *huangguoPlaybackEdgeSession {
	d.huangguoPlaybackMu.Lock()
	defer d.huangguoPlaybackMu.Unlock()
	if d.huangguoPlaybackSessions == nil {
		d.huangguoPlaybackSessions = map[string]*huangguoPlaybackEdgeSession{}
	}
	session := d.huangguoPlaybackSessions[origin]
	if session == nil {
		session = &huangguoPlaybackEdgeSession{}
		d.huangguoPlaybackSessions[origin] = session
	}
	return session
}

func (session *huangguoPlaybackEdgeSession) resolve(ctx context.Context, d *Downloader, origin, referer, videoID string, episode int) (huangguoPlaybackResponse, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if !session.valid() {
			if err := session.open(ctx, d, origin, referer); err != nil {
				return huangguoPlaybackResponse{}, fmt.Errorf("黄果 AI 播放接口握手失败: %w", err)
			}
		}
		payload, err := session.call(ctx, d, origin, referer, videoID, episode)
		if errors.Is(err, errHuangguoPlaybackSessionExpired) {
			session.reset()
			continue
		}
		if err != nil {
			return huangguoPlaybackResponse{}, fmt.Errorf("黄果 AI 播放地址请求失败: %w", err)
		}
		return payload, nil
	}
	return huangguoPlaybackResponse{}, errors.New("黄果 AI 播放会话已失效，请重试")
}

func (session *huangguoPlaybackEdgeSession) valid() bool {
	return len(session.requestKey) == 32 && len(session.responseKey) == 32 && session.expiresAt > time.Now().Unix()+5 && session.sequence < huangguoPlaybackMaxSequence
}

func (session *huangguoPlaybackEdgeSession) reset() {
	session.info, session.kid, session.sid = "", "", ""
	session.requestKey, session.responseKey = nil, nil
	session.clockOffset, session.expiresAt, session.sequence = 0, 0, 0
}

func (session *huangguoPlaybackEdgeSession) open(ctx context.Context, d *Downloader, origin, referer string) error {
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	request := huangguoPlaybackHello{Version: 1, Type: "hello", ClientPub: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())}
	status, body, err := d.postHuangguoPlayback(ctx, origin, referer, request)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("握手返回 HTTP %d", status)
	}
	var hello huangguoPlaybackHello
	if json.Unmarshal(body, &hello) != nil || hello.Version != 1 || hello.Type != "hello" ||
		!validHuangguoEdgeToken(hello.Kid, 1, 64) || !validHuangguoEdgeToken(hello.SID, 16, 128) ||
		hello.ServerTime <= 0 || hello.ExpiresAt <= hello.ServerTime || hello.ExpiresAt-hello.ServerTime > 600 {
		return errors.New("握手响应格式无效")
	}
	originURL, _ := url.Parse(origin)
	if originURL == nil || !strings.EqualFold(hello.Site, originURL.Host) {
		return errors.New("握手站点身份不一致")
	}
	serverBytes, err := base64.RawURLEncoding.DecodeString(hello.ServerPub)
	if err != nil {
		return errors.New("握手公钥编码无效")
	}
	serverKey, err := ecdh.P256().NewPublicKey(serverBytes)
	if err != nil {
		return errors.New("握手公钥无效")
	}
	shared, err := privateKey.ECDH(serverKey)
	if err != nil {
		return errors.New("握手密钥协商失败")
	}
	salt, err := base64.RawURLEncoding.DecodeString(hello.Salt)
	if err != nil || len(salt) != 32 {
		return errors.New("握手盐值无效")
	}
	info := strings.Join([]string{huangguoPlaybackProtocol, huangguoPlaybackScope, hello.Site, hello.Kid, hello.SID}, "\n")
	requestKey, err := deriveHuangguoPlaybackKey(shared, salt, info+"\nreq")
	if err != nil {
		return err
	}
	responseKey, err := deriveHuangguoPlaybackKey(shared, salt, info+"\nres")
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	session.info, session.kid, session.sid = info, hello.Kid, hello.SID
	session.requestKey, session.responseKey = requestKey, responseKey
	session.clockOffset = hello.ServerTime - now
	session.expiresAt = now + hello.ExpiresAt - hello.ServerTime
	session.sequence = 0
	return nil
}

func (session *huangguoPlaybackEdgeSession) call(ctx context.Context, d *Downloader, origin, referer, videoID string, episode int) (huangguoPlaybackResponse, error) {
	session.sequence++
	sequence := session.sequence
	if sequence > huangguoPlaybackMaxSequence {
		return huangguoPlaybackResponse{}, errHuangguoPlaybackSessionExpired
	}
	timestamp := time.Now().Unix() + session.clockOffset
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint32(nonce[8:], sequence)
	path := "/api/videos/" + videoID + "/episodes/" + strconv.Itoa(episode) + "/playback"
	packet := huangguoPlaybackBusinessPacket{Request: huangguoPlaybackBusinessRequest{
		Method: http.MethodPost, Path: path, Body: base64.StdEncoding.EncodeToString([]byte("{}")), ContentType: "application/json",
	}}
	plaintext, err := json.Marshal(packet)
	if err != nil {
		return huangguoPlaybackResponse{}, err
	}
	ciphertext, err := huangguoPlaybackSeal(session.requestKey, nonce, session.additionalData("req", sequence, timestamp), plaintext)
	if err != nil {
		return huangguoPlaybackResponse{}, err
	}
	request := huangguoPlaybackCall{Version: 1, Type: "call", Kid: session.kid, SID: session.sid, Sequence: sequence, Timestamp: timestamp, Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	status, body, err := d.postHuangguoPlayback(ctx, origin, referer, request)
	if err != nil {
		return huangguoPlaybackResponse{}, err
	}
	if status != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Error == "session_expired" {
			return huangguoPlaybackResponse{}, errHuangguoPlaybackSessionExpired
		}
		return huangguoPlaybackResponse{}, fmt.Errorf("接口返回 HTTP %d", status)
	}
	var result huangguoPlaybackCallResult
	if json.Unmarshal(body, &result) != nil || result.Version != 1 || result.Type != "result" ||
		result.Kid != session.kid || result.SID != session.sid || result.Sequence != sequence || result.Timestamp != timestamp {
		return huangguoPlaybackResponse{}, errors.New("加密响应不匹配")
	}
	encrypted, err := base64.RawURLEncoding.DecodeString(result.Ciphertext)
	if err != nil {
		return huangguoPlaybackResponse{}, errors.New("加密响应编码无效")
	}
	decoded, err := huangguoPlaybackOpen(session.responseKey, nonce, session.additionalData("res", sequence, timestamp), encrypted)
	if err != nil {
		return huangguoPlaybackResponse{}, errors.New("加密响应校验失败")
	}
	var inner huangguoPlaybackInnerResponse
	if json.Unmarshal(decoded, &inner) != nil || inner.Status < 200 || inner.Status > 599 || inner.Headers == nil {
		return huangguoPlaybackResponse{}, errors.New("接口响应格式无效")
	}
	if inner.Status < 200 || inner.Status >= 300 {
		return huangguoPlaybackResponse{}, fmt.Errorf("业务接口返回 HTTP %d", inner.Status)
	}
	content, err := base64.StdEncoding.DecodeString(inner.Body)
	if err != nil {
		return huangguoPlaybackResponse{}, errors.New("接口正文编码无效")
	}
	var envelope huangguoPlaybackEnvelope
	if json.Unmarshal(content, &envelope) != nil || envelope.Status != 1 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" || len(message) > 160 {
			message = "播放接口未返回有效数据"
		}
		return huangguoPlaybackResponse{}, errors.New(message)
	}
	return envelope.Data, nil
}

func (session *huangguoPlaybackEdgeSession) additionalData(direction string, sequence uint32, timestamp int64) []byte {
	return []byte(strings.Join([]string{session.info, direction, strconv.FormatUint(uint64(sequence), 10), strconv.FormatInt(timestamp, 10)}, "\n"))
}

func (d *Downloader) postHuangguoPlayback(ctx context.Context, origin, referer string, payload any) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	requestContext := context.WithValue(ctx, huangguoPlaybackReadKey{}, true)
	requestContext = context.WithValue(requestContext, nativeNoRedirectKey{}, true)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, origin+huangguoPlaybackRequestPath, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("Referer", referer)
	request.Header.Set("Cache-Control", "no-store")
	response, err := d.doCatalogRequestWithTimeout(request, 30*time.Second)
	if err != nil {
		return 0, nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return 0, nil, errors.Join(readErr, closeErr)
	}
	if len(content) > 512<<10 {
		return 0, nil, errors.New("接口响应过大")
	}
	if catalogResponseBlockReason(response, content) != "" {
		return 0, nil, d.catalogResponseError(request, response, content)
	}
	return response.StatusCode, content, nil
}

func replayableHuangguoPlaybackRead(request *http.Request) bool {
	if request == nil || request.Method != http.MethodPost || request.GetBody == nil || request.URL == nil || request.URL.User != nil ||
		request.URL.Scheme != "https" || request.URL.Host == "" || request.URL.Path != huangguoPlaybackRequestPath || request.URL.RawQuery != "" ||
		request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return false
	}
	origin, err := url.Parse(request.Header.Get("Origin"))
	if err != nil || origin == nil || origin.User != nil || !strings.EqualFold(origin.Host, request.URL.Host) || origin.Scheme != request.URL.Scheme {
		return false
	}
	body, err := request.GetBody()
	if err != nil {
		return false
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return false
	}
	var message struct {
		Version    int    `json:"v"`
		Type       string `json:"type"`
		ClientPub  string `json:"client_pub"`
		Kid        string `json:"kid"`
		SID        string `json:"sid"`
		Sequence   uint32 `json:"seq"`
		Timestamp  int64  `json:"ts"`
		Ciphertext string `json:"ciphertext"`
	}
	if json.Unmarshal(data, &message) != nil || message.Version != 1 {
		return false
	}
	if message.Type == "hello" {
		return len(message.ClientPub) >= 80 && len(message.ClientPub) <= 100 && message.Kid == "" && message.SID == "" && message.Ciphertext == ""
	}
	return message.Type == "call" && validHuangguoEdgeToken(message.Kid, 1, 64) && validHuangguoEdgeToken(message.SID, 16, 128) &&
		message.Sequence > 0 && message.Sequence <= huangguoPlaybackMaxSequence && message.Timestamp > 0 && len(message.Ciphertext) >= 32 && len(message.Ciphertext) <= 48<<10
}

func deriveHuangguoPlaybackKey(shared, salt []byte, info string) ([]byte, error) {
	key := make([]byte, 32)
	_, err := io.ReadFull(hkdf.New(sha256.New, shared, salt, []byte(info)), key)
	return key, err
}

func huangguoPlaybackSeal(key, nonce, additionalData, plaintext []byte) ([]byte, error) {
	aead, err := huangguoPlaybackAEAD(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, additionalData), nil
}

func huangguoPlaybackOpen(key, nonce, additionalData, ciphertext []byte) ([]byte, error) {
	aead, err := huangguoPlaybackAEAD(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, additionalData)
}

func huangguoPlaybackAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validHuangguoEdgeToken(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validPositiveDecimal(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value[0] == '0' {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validHuangguoCDNHeader(name, value string) string {
	if len(name) < 3 || len(name) > 64 || len(value) < 1 || len(value) > 1024 || !strings.HasPrefix(strings.ToLower(name), "x-") {
		return ""
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' {
			continue
		}
		return ""
	}
	return name
}

func huangguoPlaybackURLExpiry(address *url.URL) time.Time {
	parts := strings.SplitN(address.Query().Get("auth_key"), "-", 2)
	if len(parts) != 2 {
		return time.Time{}
	}
	timestamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}
	}
	expires := time.Unix(timestamp, 0)
	now := time.Now()
	if !expires.After(now) || expires.After(now.Add(24*time.Hour)) {
		return time.Time{}
	}
	return expires
}
