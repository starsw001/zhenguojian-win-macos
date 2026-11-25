package licensing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"duanjuapp/native/portable"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

type Manager struct {
	accountSession     Envelope
	mu                 sync.Mutex
	config             Config
	distribution       Distribution
	serverKey          ed25519.PublicKey
	state              State
	device             string
	loggedIn           bool
	configurationError string
	message            string
	deadline           time.Time
	lastNetwork        time.Time
	fileTimes          map[string]time.Time
	wasReady           bool
	sessionExpiresAt   int64
	sessionDeadline    time.Time
	sessionTimer       *time.Timer
	expiryRelogin      bool
}

var displayLicenseKey = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}(?:-[A-HJ-NP-Z2-9]{4}){3}$`)

// Open 创建授权管理器并加载本地状态，保留初始化错误供状态查询。
func Open(config Config) *Manager {
	m := &Manager{config: config, fileTimes: map[string]time.Time{}}
	if m.config.Now == nil {
		m.config.Now = time.Now
	}
	if m.config.Device == nil {
		m.config.Device = machineHash
	}
	if m.config.DeviceInfo == nil {
		m.config.DeviceInfo = platformDeviceInfo
	}
	if m.config.Protect == nil {
		m.config.Protect = platformProtect
	}
	if m.config.Unprotect == nil {
		m.config.Unprotect = platformUnprotect
	}
	if m.config.Client == nil {
		m.config.Client = defaultLicenseClient()
	}
	if err := m.open(); err != nil {
		m.configurationError = err.Error()
	}
	return m
}

// open 验证分发清单、公钥与设备身份，并初始化授权存储。
func (m *Manager) open() error {
	key, err := decodeKey(m.config.DistributionPublicKey)
	if err != nil {
		return err
	}
	m.serverKey, err = decodeKey(m.config.ServerPublicKey)
	if err != nil {
		return err
	}
	origin, err := url.Parse(m.config.ServerURL)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" && origin.Path != "/" {
		return errors.New("许可证服务地址配置无效")
	}
	for _, path := range []string{m.config.ActivatePath, m.config.VerifyPath, m.config.EventPath} {
		if !strings.HasPrefix(path, "/api/") || strings.ContainsAny(path, "?#\\") || strings.Contains(path, "..") {
			return errors.New("许可证接口契约尚未配置")
		}
	}
	body, err := os.ReadFile(filepath.Join(m.config.Root, "distribution.json"))
	if err != nil || len(body) > 512<<10 {
		return errors.New("缺少专属分发签名，请向管理员取得完整安装包")
	}
	var envelope Envelope
	if json.Unmarshal(body, &envelope) != nil || verifyEnvelope(envelope, key, &m.distribution) != nil {
		return errors.New("专属分发签名无效，软件已禁止使用")
	}
	d := m.distribution
	var permission ActivationPermit
	if verifyEnvelope(d.Permit, key, &permission) != nil || permission.Category != DownloadLicenseCategory || permission.Category != d.Category || permission.Protocol != Protocol || permission.Kind != "activation-permit" || permission.DistributionID != d.DistributionID || permission.Edition != d.Edition || permission.Months != d.Months || permission.Username != d.Username || permission.AccountMode != d.AccountMode {
		return errors.New("缺少与当前分发一致的发布者激活许可")
	}
	if d.Category != DownloadLicenseCategory || d.Protocol != Protocol || !regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`).MatchString(d.DistributionID) || d.ReleaseID == "" || d.Edition != m.config.Edition || d.Version != m.config.Version || d.AccountMode != "" && d.AccountMode != "universal" || d.AccountMode == "" && (!regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(d.Username) || d.Username != strings.ToLower(strings.TrimSpace(d.Username))) || d.Months < 1 || d.Months > 12 || d.AccountMode == "" && (d.Iterations < 200000 || d.Iterations > 1000000) || d.AccountMode == "universal" && (d.Username != "" || d.PasswordHash != "" || d.Salt != "" || d.Iterations != 0 || d.Months != 12) || len(d.Files) < 3 {
		return errors.New("分发配置与本客户端不匹配")
	}
	for _, name := range []string{m.config.Edition + ".exe", "duanju_core.dll", "data/app.so"} {
		if d.Files[name] == "" {
			return errors.New("分发完整性清单缺少必要运行文件")
		}
	}
	m.device, err = m.config.Device()
	if err != nil || len(m.device) != 64 {
		return errors.New("无法取得本机授权摘要")
	}
	m.config.Storage = filepath.Join(m.config.Storage, d.DistributionID)
	if err := portable.EnsureDirectory(m.config.Storage); err != nil {
		return errors.New("无法创建独立授权目录")
	}
	unlock, err := m.beginState()
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.integrity(true); err != nil {
		m.lock("integrity_mismatch")
		return nil
	}
	return nil
}

// integrity 核对签名清单中的运行文件，按 full 决定是否复用已记录的文件时间。
func (m *Manager) integrity(full bool) error {
	root, err := filepath.Abs(m.config.Root)
	if err != nil {
		return err
	}
	for name, expected := range m.distribution.Files {
		if !validRelative(name) || len(expected) != 64 {
			return errors.New("完整性清单路径无效")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		for parent := path; parent != root; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("完整性清单文件缺失或路径异常")
			}
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("完整性清单文件不可用")
		}
		if !full && m.fileTimes[name].Equal(info.ModTime()) {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		actual, err := hashReader(file)
		file.Close()
		if err != nil || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return errors.New("运行文件完整性校验失败")
		}
		m.fileTimes[name] = info.ModTime()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !entry.IsDir() && (ext == ".dll" || ext == ".exe") && m.distribution.Files[entry.Name()] == "" {
			return errors.New("发现清单外运行模块")
		}
	}
	return nil
}

// save 将当前授权状态经过平台数据保护后写入本地存储。
func (m *Manager) save() error {
	body, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	body, err = m.config.Protect(body)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(m.config.Storage, ".license-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if err := RestrictPrivateFile(name); err != nil {
		temp.Close()
		return err
	}
	_, err = temp.Write(body)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(m.config.Storage, "license-state.dat"))
}

// lock 锁定授权会话并清理授权相关的会话数据。
func (m *Manager) lock(reason string) {
	if m.state.Locked != "" {
		return
	}
	m.state.Locked = reason
	m.state.RememberedLogin = ""
	m.loggedIn = false
	m.deadline = time.Time{}
	m.clearSessionDeadline()
	if m.state.Event == nil {
		id, err := randomID()
		if err == nil {
			m.state.Event = &Event{ID: id, Reason: reason, ReleaseID: m.distribution.ReleaseID, Version: m.config.Version, CreatedAt: m.config.Now().Unix(), Attestation: m.state.Lease}
		}
	}
	m.state.Lease = Envelope{}
	if err := m.save(); err != nil {
		m.message = "软件已锁定，但本地锁定状态保存失败，请联系管理员"
	}
	if m.config.OnLock != nil {
		go m.config.OnLock()
	}
}

// clearSessionDeadline 清除当前会话的到期定时器与截止时间。
func (m *Manager) clearSessionDeadline() {
	if m.sessionTimer != nil {
		m.sessionTimer.Stop()
		m.sessionTimer = nil
	}
	m.sessionExpiresAt, m.sessionDeadline = 0, time.Time{}
}

// setSessionDeadline 根据已签名授权设置当前会话的到期截止时间。
func (m *Manager) setSessionDeadline(expiresAt int64) {
	if m.sessionExpiresAt == expiresAt {
		return
	}
	m.clearSessionDeadline()
	m.sessionExpiresAt = expiresAt
	now := m.config.Now()
	delay := time.Unix(expiresAt, 0).Sub(now)
	m.sessionDeadline = now.Add(delay)
	m.sessionTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.expireSession()
	})
}

// expireSession 使已到期会话退出可用状态。
func (m *Manager) expireSession() {
	now := m.config.Now()
	if !m.loggedIn || m.sessionExpiresAt == 0 || now.Unix() < m.sessionExpiresAt && now.Before(m.sessionDeadline) {
		return
	}
	m.loggedIn, m.wasReady, m.expiryRelogin = false, false, true
	m.clearSessionDeadline()
	m.deadline = time.Time{}
	m.message = ""
	if m.config.OnLock != nil {
		go m.config.OnLock()
	}
}

// status 汇总登录、授权、到期与重试信息供客户端显示。
func (m *Manager) status() Status {
	m.expireSession()
	nowSeconds := m.config.Now().Unix()
	s := Status{Edition: m.config.Edition, Version: m.config.Version, DistributionID: m.distribution.DistributionID, CheckedAt: nowSeconds, RetryLoginAt: m.state.RetryLoginAt, RetryNetworkAt: m.state.RetryNetworkAt, RetryNetworkRemainingSeconds: max(int64(0), m.state.RetryNetworkAt-nowSeconds), RetryLoginRemainingSeconds: max(int64(0), m.state.RetryLoginAt-nowSeconds)}
	if m.configurationError != "" {
		s.Phase, s.Message = "configuration", m.configurationError
		return s
	}
	if m.state.Locked != "" {
		s.Phase, s.Message = "locked", "软件完整性异常，已锁定。仅授权缓存停止使用，原视频和记录保留；请联系管理员修复。"
		return s
	}
	if !m.loggedIn || m.state.PasswordResetRequired {
		s.Phase, s.Message = "login", "请输入您的专属用户名和密码"
		if m.state.PasswordResetRequired {
			s.Message = "账号密码已被重置，已退出。请重新输入用户名和新密码。"
		} else if m.expiryRelogin {
			s.Message = "许可证已到期，已退出。请重新登录，管理员续期后再联网复核。"
		}
		return s
	}
	s.Username = m.currentUsername()
	s.CheckedAt = m.config.Now().Unix()
	if m.state.Lease.Payload == "" {
		s.Phase, s.Message = "activation", "登录成功，请输入许可证 key 激活当前电脑"
		return s
	}
	lease, err := m.readLease(m.state.Lease, "")
	if err != nil {
		s.Phase, s.Message = "activation", "已保存的授权签名无效，请重新联网激活"
		return s
	}
	s.ExpiresAt, s.RefreshAt = lease.ExpiresAt, lease.RefreshAt
	s.RemainingSeconds = max(int64(0), lease.ExpiresAt-s.CheckedAt)
	now := m.config.Now()
	if now.Unix() < m.state.HighestTime-300 {
		s.Phase, s.Message = "verify", "系统时间发生回退，必须联网复核授权"
		return s
	}
	if lease.Status == "revoked" {
		s.Phase, s.Message = "revoked", "许可证已停用或设备授权已解除，请联系管理员"
		return s
	}
	if lease.Status == "expired" || now.Unix() >= lease.ExpiresAt {
		s.Phase, s.Message = "renewal", "许可证已到期，请联系管理员续期。续期后联网复核原许可证即可。"
		return s
	}
	if m.state.NeedsVerification || m.deadline.IsZero() {
		s.Phase, s.Message = "verify", "需要联网复核许可证后才能使用软件"
		return s
	}
	if now.Unix() >= lease.RefreshAt || !now.Before(m.deadline) {
		s.Phase, s.Message = "verify", "已到每日授权复核时间，请联网验证；不会删除视频和记录"
		return s
	}
	s.Phase, s.Message, s.Ready = "ready", "授权有效", true
	s.LicenseKey = lease.LicenseKey
	m.setSessionDeadline(lease.ExpiresAt)
	return s
}

// readLease 验证服务端授权租约的签名、请求关联与身份字段。
func (m *Manager) readLease(envelope Envelope, nonce string) (Lease, error) {
	var lease Lease
	if err := verifyEnvelope(envelope, m.serverKey, &lease); err != nil {
		return lease, err
	}
	if lease.LicenseEdition != m.config.Edition {
		return lease, errors.New("此许可证不适用于当前软件，红果赏和真果赏需要分别激活")
	}
	if lease.Category != DownloadLicenseCategory {
		return lease, errors.New("许可证类别不匹配：本软件仅支持下载类许可证")
	}
	if lease.Protocol != Protocol || lease.LicenseID == "" || lease.Username != m.currentUsername() || lease.DistributionID != m.distribution.DistributionID || lease.Edition != m.config.Edition || lease.DeviceHash != m.device || lease.Months < 1 || lease.Months > 12 || lease.ActivatedAt <= 0 || lease.IssuedAt < lease.ActivatedAt || lease.ExpiresAt <= lease.ActivatedAt || nonce != "" && lease.Nonce != nonce || lease.LicenseKey != "" && (lease.Status != "active" || !displayLicenseKey.MatchString(lease.LicenseKey)) {
		return lease, errors.New("许可证不属于本机、当前分发或有效期无效")
	}
	if lease.Status != "active" && lease.Status != "expired" && lease.Status != "revoked" && lease.Status != "password_reset" {
		return lease, errors.New("许可证状态无效")
	}
	anchor := lease.ActivatedAt
	if lease.RenewalRevision == 0 {
		if lease.TermStartedAt != 0 || lease.Months > m.distribution.Months {
			return lease, errors.New("首次激活期限超过发布者许可")
		}
	} else {
		if lease.RenewalRevision < 0 || lease.TermStartedAt <= lease.ActivatedAt || lease.ExpiresAt > addLicenseMonths(time.Unix(lease.IssuedAt, 0), 12).Unix() {
			return lease, errors.New("续期版本或期限无效")
		}
		anchor = lease.TermStartedAt
	}
	if lease.ExpiresAt <= anchor || lease.ExpiresAt > addLicenseMonths(time.Unix(anchor, 0), lease.Months).Unix() {
		return lease, errors.New("许可证有效期超过签名期限的指定月数")
	}
	if lease.Status == "active" && (lease.ExpiresAt <= lease.IssuedAt || lease.RefreshAt <= lease.IssuedAt || lease.RefreshAt > lease.IssuedAt+24*60*60 || lease.RefreshAt > lease.ExpiresAt) {
		return lease, errors.New("每日授权租约时间无效")
	}
	if lease.CredentialSalt != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`).MatchString(lease.CredentialSalt) {
		return lease, errors.New("账号凭据版本无效")
	}
	return lease, nil
}

// addLicenseMonths 按授权月份规则计算新的到期时间。
func addLicenseMonths(start time.Time, months int) time.Time {
	start = start.UTC()
	first := time.Date(start.Year(), start.Month()+time.Month(months), 1, start.Hour(), start.Minute(), start.Second(), 0, time.UTC)
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := start.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, first.Hour(), first.Minute(), first.Second(), 0, time.UTC)
}

// Login 验证账号登录资料并更新当前授权会话状态。
func (m *Manager) Login(username, password string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configurationError != "" {
		return m.status()
	}
	unlock, err := m.beginState()
	if err != nil {
		m.configurationError = err.Error()
		return m.status()
	}
	defer unlock()
	m.expireSession()
	if m.configurationError != "" || m.state.Locked != "" {
		return m.status()
	}
	now := m.config.Now().Unix()
	if now < m.state.RetryLoginAt {
		s := m.status()
		s.Message = "尝试过于频繁，请稍后重试"
		return s
	}
	if m.distribution.AccountMode == "universal" {
		return m.loginUniversal(username, password)
	}
	salt, err := base64.RawURLEncoding.DecodeString(m.distribution.Salt)
	expected, hashErr := base64.RawURLEncoding.DecodeString(m.distribution.PasswordHash)
	if err != nil || hashErr != nil || len(salt) < 16 || len(expected) != 32 {
		m.configurationError = "离线登录凭据配置无效"
		return m.status()
	}
	actual := pbkdf2.Key([]byte(password), salt, m.distribution.Iterations, 32, sha256.New)
	nameHash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	expectedName := sha256.Sum256([]byte(m.distribution.Username))
	if subtle.ConstantTimeCompare(actual, expected) != 1 || subtle.ConstantTimeCompare(nameHash[:], expectedName[:]) != 1 {
		m.state.FailedLogins++
		if m.state.FailedLogins >= 5 {
			m.state.RetryLoginAt = now + 60
		}
		if err := m.save(); err != nil {
			m.message = "无法保存登录保护状态"
		}
		s := m.status()
		s.Message = "用户名或密码不正确"
		return s
	}
	m.loggedIn = true
	m.state.FailedLogins, m.state.RetryLoginAt = 0, 0
	m.state.RememberedLogin = m.loginBinding()
	if err := m.save(); err != nil {
		m.loggedIn = false
		s := m.status()
		s.Message = "无法保存授权状态，请检查授权目录权限"
		return s
	}
	m.expiryRelogin = false
	m.message = ""
	s := m.status()
	m.wasReady = s.Ready
	return s
}

// Allowed 判断当前授权状态是否允许使用业务功能。
func (m *Manager) Allowed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status().Ready
}

// Poll 按当前复核策略刷新并返回授权状态。
func (m *Manager) Poll(refresh bool) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configurationError != "" {
		return m.status()
	}
	unlock, err := m.beginState()
	if err != nil {
		m.configurationError = err.Error()
		return m.status()
	}
	defer unlock()
	defer m.updateReadiness()
	if m.configurationError != "" {
		return m.status()
	}
	if m.state.Locked == "" {
		if err := m.integrity(false); err != nil {
			m.lock("integrity_mismatch")
		}
	}
	if m.state.Locked != "" {
		m.reportEvent()
		return m.status()
	}
	status := m.status()
	now := m.config.Now().Unix()
	if refresh && m.loggedIn && status.Phase == "verify" && now >= m.state.NextAutoAttempt && now >= m.state.RetryNetworkAt {
		m.refresh("")
		status = m.status()
	}
	if status.Ready && m.config.Now().Unix() > m.state.HighestTime {
		m.state.HighestTime = m.config.Now().Unix()
		if err := m.save(); err != nil {
			m.deadline = time.Time{}
			status = m.status()
			status.Message = "无法保存每日授权保护状态，请检查目录权限"
		}
	}
	if m.message != "" && !m.expiryRelogin {
		status.Message = m.message
	}
	return status
}

// Activate 使用许可证 key 发起本机激活，并更新服务端授权结果。
func (m *Manager) Activate(key string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configurationError != "" {
		return m.status()
	}
	unlock, err := m.beginState()
	if err != nil {
		m.configurationError = err.Error()
		return m.status()
	}
	defer unlock()
	defer m.updateReadiness()
	m.expireSession()
	if m.configurationError != "" || m.state.Locked != "" || !m.loggedIn {
		return m.status()
	}
	if key == "" || len(key) > 512 {
		s := m.status()
		s.Message = "请输入有效许可证 key"
		return s
	}
	if m.config.Now().Unix() < m.state.RetryNetworkAt {
		s := m.status()
		s.Message = m.retryNetworkMessage()
		return s
	}
	m.refresh(key)
	s := m.status()
	if m.message != "" && !m.expiryRelogin {
		s.Message = m.message
	}
	return s
}

// Verify 按当前校验策略检查许可证并更新会话状态。
func (m *Manager) Verify() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configurationError != "" {
		return m.status()
	}
	unlock, err := m.beginState()
	if err != nil {
		m.configurationError = err.Error()
		return m.status()
	}
	defer unlock()
	defer m.updateReadiness()
	m.expireSession()
	if m.configurationError != "" || m.state.Locked != "" || !m.loggedIn || m.state.Lease.Payload == "" {
		return m.status()
	}
	if status := m.status(); status.Ready {
		return status
	}
	if m.config.Now().Unix() < m.state.RetryNetworkAt {
		s := m.status()
		s.Message = m.retryNetworkMessage()
		return s
	}
	m.refresh("")
	s := m.status()
	if m.message != "" && !m.expiryRelogin {
		s.Message = m.message
	}
	return s
}

// retryNetworkMessage 生成授权请求失败后的网络与重试提示。
func (m *Manager) retryNetworkMessage() string {
	seconds := max(int64(0), m.state.RetryNetworkAt-m.config.Now().Unix())
	return fmt.Sprintf("授权请求等待中，请在 %d 分 %02d 秒后重试", seconds/60, seconds%60)
}

// post 向配置的授权 API 提交 JSON 请求并解析响应。
func (m *Manager) post(path string, body any, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(m.config.ServerURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "GuoApp-License/"+m.config.Version)
	response, err := sendLicenseRequest(m.config.Client, request)
	if err != nil {
		return licenseConnectionError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 429 {
		wait := int64(60)
		retryAfter := strings.TrimSpace(response.Header.Get("Retry-After"))
		if seconds, err := strconv.ParseInt(retryAfter, 10, 64); err == nil && seconds > 0 {
			wait = seconds
		} else if date, err := http.ParseTime(retryAfter); err == nil && date.After(m.config.Now()) {
			wait = date.Unix() - m.config.Now().Unix()
		}
		if wait > 24*60*60 {
			wait = 24 * 60 * 60
		}
		m.state.RetryNetworkAt = m.config.Now().Unix() + wait
		_ = m.save()
		return errors.New(m.retryNetworkMessage())
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return errors.New("许可证服务响应无效")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(data, &failure) == nil {
			messages := map[string]string{
				"license_edition_mismatch": "此许可证不适用于当前软件，请分别使用红果赏和真果赏的许可证",
				"license_edition_conflict": "此许可证曾用于两个软件，请分别重新生成许可证",
				"category_mismatch":        "许可证类别不匹配：红果赏和真果赏需要下载类许可证，不能使用创作类许可证",
				"invalid_login":            "用户名或密码不正确，或登录已超时，请重新登录",
				"login_rate_limited":       "登录尝试过于频繁，请稍后再试",
				"invalid_username":         "请使用当前专属分发的用户名激活或复核",
				"username_mismatch":        "用户名与许可证不匹配，请核对管理员发放的同一份资料",
				"invalid_expiry":           "许可证有效期与服务端记录不匹配，请联系管理员",
				"device_limit":             "此许可证已绑定其他电脑，请联系管理员换机解绑",
				"device_released":          "此电脑已被管理员解除授权，请联系管理员恢复或使用新电脑",
				"license_expired":          "许可证已过期，请联系管理员续期后复核原许可证",
				"upgrade_required":         "此许可证已续期，请使用支持续期的新版本",
				"license_disabled":         "此许可证已被管理员停用",
				"distribution_conflict":    "许可证与此专属分发不匹配，请联系管理员",
				"license_not_found":        "未找到此许可证，请核对 key 或稍后重试",
				"invalid_permit":           "发布者激活许可与此软件不匹配，请取得完整安装包",
				"legacy_device_active":     "此 key 仍绑定旧设备，请先由管理员释放旧设备",
			}
			if message := messages[failure.Code]; message != "" {
				return &licenseServiceError{Code: failure.Code, Message: message}
			}
		}
		return fmt.Errorf("许可证服务未接受请求（HTTP %d），请联系管理员", response.StatusCode)
	}
	if json.Unmarshal(data, target) != nil {
		return errors.New("许可证接口契约不匹配")
	}
	return nil
}

type licenseServiceError struct {
	Code    string
	Message string
}

// Error 返回当前错误的可显示文本。
func (e *licenseServiceError) Error() string { return e.Message }

// requirePasswordLogin 清除失效登录状态并要求重新输入账号密码。
func (m *Manager) requirePasswordLogin() {
	m.loggedIn = false
	m.accountSession = Envelope{}
	m.expiryRelogin = false
	m.state.PasswordResetRequired = true
	m.state.RememberedLogin = ""
	m.state.NeedsVerification = true
	m.state.RetryNetworkAt = 0
	m.state.NextAutoAttempt = 0
	m.message = ""
	m.deadline = time.Time{}
	m.clearSessionDeadline()
	if m.save() != nil {
		m.configurationError = "无法保存退出状态，授权已停止使用，请检查授权目录权限"
	}
}

// refresh 刷新当前许可证的服务端租约与账号状态。
func (m *Manager) refresh(key string) {
	m.lastNetwork = m.config.Now()
	m.message = ""
	m.state.NextAutoAttempt = m.lastNetwork.Unix() + 24*60*60
	m.state.RetryNetworkAt = m.lastNetwork.Unix() + 60
	m.state.NeedsVerification = true
	if m.save() != nil {
		m.message = "无法保存授权请求保护状态，未发送联网请求"
		return
	}
	nonce, err := randomID()
	if err != nil {
		m.message = "无法生成授权随机挑战"
		return
	}
	path := m.config.VerifyPath
	body := map[string]any{"protocol": Protocol, "distributionId": m.distribution.DistributionID, "username": m.currentUsername(), "edition": m.config.Edition, "version": m.config.Version, "deviceHash": m.device, "nonce": nonce, "lease": m.state.Lease}
	if strings.HasSuffix(m.config.VerifyPath, "-account") && strings.HasSuffix(m.config.ActivatePath, "-account") {
		body["renewalVersion"] = 1
	}
	if key != "" {
		path = m.config.ActivatePath
		body["key"] = key
		body["permit"] = m.distribution.Permit
		body["deviceInfo"] = m.config.DeviceInfo()
		if m.distribution.AccountMode == "universal" {
			body["accountSession"] = m.accountSession
		}
	}
	var envelope Envelope
	err = m.post(path, body, &envelope)
	m.expireSession()
	if err != nil {
		var serviceError *licenseServiceError
		if m.distribution.AccountMode == "universal" && errors.As(err, &serviceError) && serviceError.Code == "invalid_login" {
			m.requirePasswordLogin()
		} else {
			m.message = err.Error()
		}
		return
	}
	lease, err := m.readLease(envelope, nonce)
	if err != nil || lease.IssuedAt < m.state.HighestTime-300 || absSeconds(lease.IssuedAt-m.config.Now().Unix()) > 300 {
		m.message = "授权签名、随机挑战或服务时间校验失败，请校准系统时间后重试"
		return
	}
	previous := m.state
	if old, err := m.readLease(previous.Lease, ""); err == nil && (key == "" || old.LicenseID == lease.LicenseID || m.distribution.AccountMode != "universal") {
		sameTerm := lease.RenewalRevision == old.RenewalRevision && lease.ExpiresAt == old.ExpiresAt && lease.TermStartedAt == old.TermStartedAt && lease.Months == old.Months
		renewed := lease.RenewalRevision > old.RenewalRevision && lease.TermStartedAt >= old.ExpiresAt && lease.ExpiresAt > old.ExpiresAt
		if lease.ActivatedAt != old.ActivatedAt || lease.LicenseID != old.LicenseID || !sameTerm && !renewed || lease.LicenseKey != "" && old.LicenseKey != "" && lease.LicenseKey != old.LicenseKey {
			m.message = "许可证首次激活或续期版本与服务端签名不一致"
			return
		}
	}
	if lease.Status == "password_reset" {
		m.requirePasswordLogin()
		return
	}
	m.state.Lease = envelope
	m.state.HighestTime = lease.IssuedAt
	m.state.RetryNetworkAt = 0
	m.state.NeedsVerification = false
	if lease.Status == "active" && m.loggedIn && !m.expiryRelogin {
		m.state.RememberedLogin = m.loginBinding()
	} else {
		m.state.RememberedLogin = ""
	}
	if lease.Status == "active" {
		m.state.NextAutoAttempt = lease.RefreshAt
	}
	if err := m.save(); err != nil {
		m.state = previous
		m.message = "无法安全保存激活结果，软件未解锁"
		return
	}
	m.deadline = m.config.Now().Add(time.Duration(lease.RefreshAt-m.config.Now().Unix()) * time.Second)
}

// absSeconds 取得秒数的绝对值。
func absSeconds(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// reportEvent 尝试上报待提交的完整性事件并维护去重状态。
func (m *Manager) reportEvent() {
	event := m.state.Event
	if event == nil || event.Reported || event.Attestation.Payload == "" || m.config.Now().Unix()-event.LastAttempt < 24*60*60 {
		return
	}
	event.LastAttempt = m.config.Now().Unix()
	if m.save() != nil {
		return
	}
	var response struct {
		EventID  string `json:"eventId"`
		Accepted bool   `json:"accepted"`
	}
	body := map[string]any{"protocol": Protocol, "distributionId": m.distribution.DistributionID, "edition": m.config.Edition, "deviceHash": m.device, "lease": event.Attestation, "eventId": event.ID, "reason": event.Reason, "releaseId": event.ReleaseID, "version": event.Version}
	if m.post(m.config.EventPath, body, &response) == nil && response.Accepted && response.EventID == event.ID {
		event.Reported = true
		event.Attestation = Envelope{}
		_ = m.save()
	}
}
