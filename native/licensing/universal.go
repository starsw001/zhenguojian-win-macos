package licensing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/pbkdf2"
	"regexp"
	"strings"
	"time"
)

type accountSessionClaims struct {
	CredentialSalt string `json:"credentialSalt"`
	Protocol       string `json:"protocol"`
	Kind           string `json:"kind"`
	Username       string `json:"username"`
	DistributionID string `json:"distributionId"`
	Edition        string `json:"edition"`
	DeviceHash     string `json:"deviceHash"`
	Nonce          string `json:"nonce"`
	IssuedAt       int64  `json:"issuedAt"`
	ExpiresAt      int64  `json:"expiresAt"`
}

// currentUsername 读取当前通用分发会话使用的账号。
func (m *Manager) currentUsername() string {
	if m.distribution.AccountMode == "universal" {
		return m.state.AuthUsername
	}
	return m.distribution.Username
}

// loginUniversal 处理通用分发模式的账号登录与凭据更新。
func (m *Manager) loginUniversal(username, password string) Status {
	name := strings.ToLower(strings.TrimSpace(username))
	failure := func(err error) Status {
		s := m.status()
		s.Message = err.Error()
		return s
	}
	if !regexp.MustCompile(`^[a-z0-9]{6,12}@[a-z0-9]{2,12}\.com$`).MatchString(name) || len(password) != 8 {
		return failure(errors.New("请输入专属用户名和 8 位密码"))
	}
	if m.config.Now().Unix() < m.state.RetryNetworkAt {
		s := m.status()
		s.Message = m.retryNetworkMessage()
		return s
	}
	nonce, err := randomID()
	if err != nil {
		return failure(errors.New("无法生成登录挑战"))
	}
	body := map[string]any{"protocol": Protocol, "distributionId": m.distribution.DistributionID, "edition": m.config.Edition, "version": m.config.Version, "deviceHash": m.device, "username": name, "nonce": nonce, "permit": m.distribution.Permit}
	var challenge struct {
		ID         string `json:"challengeId"`
		Salt       string `json:"salt"`
		Iterations int    `json:"iterations"`
		ExpiresAt  int64  `json:"expiresAt"`
	}
	if err = m.post("/api/guoapp/v1/account-challenge", body, &challenge); err != nil {
		return failure(err)
	}
	salt, err := base64.RawURLEncoding.DecodeString(challenge.Salt)
	now := m.config.Now().Unix()
	if err != nil || len(salt) != 24 || challenge.Iterations != 310000 || len(challenge.ID) != 32 || challenge.ExpiresAt <= now || challenge.ExpiresAt > now+300 {
		return failure(errors.New("登录挑战无效，请检查系统时间或稍后再试"))
	}
	verifier := pbkdf2.Key([]byte(password), salt, challenge.Iterations, 32, sha256.New)
	transcript, _ := json.Marshal([]string{challenge.ID, name, m.distribution.DistributionID, m.config.Edition, m.device, nonce})
	mac := hmac.New(sha256.New, verifier)
	_, _ = mac.Write(transcript)
	body["challengeId"], body["proof"] = challenge.ID, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	var envelope Envelope
	if err = m.post("/api/guoapp/v1/account-login", body, &envelope); err != nil {
		var rejection *licenseServiceError
		if errors.As(err, &rejection) && rejection.Code == "invalid_login" {
			if m.state.RetryLoginAt != 0 && now >= m.state.RetryLoginAt {
				m.state.FailedLogins = 0
				m.state.RetryLoginAt = 0
			}
			m.state.FailedLogins++
			if m.state.FailedLogins >= 5 {
				m.state.RetryLoginAt = m.config.Now().Unix() + 300
			}
			if m.save() != nil {
				return failure(errors.New("无法保存登录保护状态，请检查授权目录权限"))
			}
		}
		return failure(err)
	}
	var session accountSessionClaims
	if verifyEnvelope(envelope, m.serverKey, &session) != nil || session.CredentialSalt != challenge.Salt || session.Protocol != Protocol || session.Kind != "account-session" || session.Username != name || session.DistributionID != m.distribution.DistributionID || session.Edition != m.config.Edition || session.DeviceHash != m.device || session.Nonce != nonce || absSeconds(session.IssuedAt-m.config.Now().Unix()) > 300 || session.ExpiresAt-session.IssuedAt != 600 || session.ExpiresAt <= m.config.Now().Unix() {
		return failure(errors.New("登录签名或设备绑定不一致"))
	}
	if m.state.AuthUsername != name {
		m.state.Lease = Envelope{}
		m.state.RememberedLogin = ""
		m.state.NeedsVerification = false
		m.state.NextAutoAttempt = 0
		m.deadline = time.Time{}
		m.clearSessionDeadline()
	}
	priorLease, priorLeaseError := m.readLease(m.state.Lease, "")
	m.state.PasswordResetRequired = false
	m.state.RememberedLogin = ""
	if m.state.Lease.Payload != "" {
		m.state.NeedsVerification = true
		m.state.NextAutoAttempt = 0
		m.deadline = time.Time{}
	}
	m.state.AuthUsername = name
	m.accountSession = envelope
	m.loggedIn = true
	m.expiryRelogin = false
	m.message = ""
	m.state.FailedLogins, m.state.RetryLoginAt, m.state.RetryNetworkAt = 0, 0, 0
	if err = m.save(); err != nil {
		m.loggedIn = false
		return failure(errors.New("无法安全保存登录状态"))
	}
	if priorLeaseError == nil && priorLease.Username == name && priorLease.LicenseKey != "" {
		m.refresh(priorLease.LicenseKey)
	}
	s := m.status()
	if m.message != "" {
		s.Message = m.message
	}
	return s
}
