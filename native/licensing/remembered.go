package licensing

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// loginBinding 计算记住登录所需的账号、设备与分发绑定信息。
func (m *Manager) loginBinding() string {
	d := m.distribution
	body, _ := json.Marshal([]any{Protocol, d.DistributionID, d.Edition, m.currentUsername(), d.Salt, d.PasswordHash, d.Iterations, m.device, m.config.DistributionPublicKey, m.config.ServerPublicKey})
	digest := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// beginState 取得授权状态锁并载入状态，返回完成时的释放函数。
func (m *Manager) beginState() (func(), error) {
	unlock, err := lockLicenseState(filepath.Join(m.config.Storage, ".license-state.lock"))
	if err != nil {
		return nil, errors.New("无法锁定本机授权状态，请稍后重试")
	}
	m.expireSession()
	previous := m.state
	state := State{DistributionID: m.distribution.DistributionID}
	body, err := os.ReadFile(filepath.Join(m.config.Storage, "license-state.dat"))
	if err == nil {
		plain, decodeErr := m.config.Unprotect(body)
		if len(body) > 512<<10 || decodeErr != nil || json.Unmarshal(plain, &state) != nil || state.DistributionID != m.distribution.DistributionID {
			unlock()
			return nil, errors.New("本地授权状态不属于当前用户或分发，请联系管理员；原记录未改动")
		}
	} else if !os.IsNotExist(err) {
		unlock()
		return nil, errors.New("无法读取本地授权状态")
	}
	state.HighestTime = max(state.HighestTime, previous.HighestTime)
	m.state = state
	if state.PasswordResetRequired {
		m.loggedIn = false
		m.accountSession = Envelope{}
		m.clearSessionDeadline()
	}
	lease, leaseErr := m.readLease(state.Lease, "")
	now := m.config.Now()
	if leaseErr == nil && lease.Status == "active" && now.Unix() < lease.ExpiresAt && state.Locked == "" {
		if previous.Lease != state.Lease || m.deadline.IsZero() {
			m.deadline = now.Add(time.Duration(lease.RefreshAt-now.Unix()) * time.Second)
		}
		if !m.loggedIn && !m.expiryRelogin && subtle.ConstantTimeCompare([]byte(state.RememberedLogin), []byte(m.loginBinding())) == 1 {
			m.loggedIn = true
		}
		if m.loggedIn && !m.expiryRelogin {
			m.setSessionDeadline(lease.ExpiresAt)
		}
	} else {
		m.deadline = time.Time{}
	}
	return unlock, nil
}

// updateReadiness 更新授权可用状态及对应的就绪标记。
func (m *Manager) updateReadiness() {
	ready := m.status().Ready
	if m.wasReady && !ready && m.config.OnLock != nil {
		go m.config.OnLock()
	}
	m.wasReady = ready
}
