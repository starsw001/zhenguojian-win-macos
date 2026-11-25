package licensing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const Protocol = "guoapp-license-v1"
const DownloadLicenseCategory = 1

type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type Distribution struct {
	Category       int               `json:"category"`
	AccountMode    string            `json:"accountMode,omitempty"`
	Permit         Envelope          `json:"permit"`
	Protocol       string            `json:"protocol"`
	DistributionID string            `json:"distributionId"`
	ReleaseID      string            `json:"releaseId"`
	Edition        string            `json:"edition"`
	Version        string            `json:"version"`
	Username       string            `json:"username"`
	Salt           string            `json:"salt"`
	PasswordHash   string            `json:"passwordHash"`
	Iterations     int               `json:"iterations"`
	Months         int               `json:"months"`
	Files          map[string]string `json:"files"`
}

type ActivationPermit struct {
	Category       int    `json:"category"`
	AccountMode    string `json:"accountMode,omitempty"`
	Protocol       string `json:"protocol"`
	Kind           string `json:"kind"`
	DistributionID string `json:"distributionId"`
	Edition        string `json:"edition"`
	Months         int    `json:"months"`
	Username       string `json:"username,omitempty"`
}

type Lease struct {
	CredentialSalt  string `json:"credentialSalt,omitempty"`
	LicenseEdition  string `json:"licenseEdition"`
	Category        int    `json:"category"`
	Protocol        string `json:"protocol"`
	LicenseID       string `json:"licenseId"`
	DistributionID  string `json:"distributionId"`
	Edition         string `json:"edition"`
	DeviceHash      string `json:"deviceHash"`
	Nonce           string `json:"nonce"`
	Status          string `json:"status"`
	IssuedAt        int64  `json:"issuedAt"`
	ActivatedAt     int64  `json:"firstActivatedAt"`
	ExpiresAt       int64  `json:"expiresAt"`
	RefreshAt       int64  `json:"refreshAt"`
	Months          int    `json:"months"`
	Username        string `json:"username,omitempty"`
	LicenseKey      string `json:"licenseKey,omitempty"`
	RenewalRevision int64  `json:"renewalRevision,omitempty"`
	TermStartedAt   int64  `json:"termStartedAt,omitempty"`
}

type Event struct {
	ID          string   `json:"eventId"`
	Reason      string   `json:"reason"`
	ReleaseID   string   `json:"releaseId"`
	Version     string   `json:"version"`
	CreatedAt   int64    `json:"createdAt"`
	Reported    bool     `json:"reported"`
	LastAttempt int64    `json:"lastAttempt"`
	Attestation Envelope `json:"attestation"`
}

type State struct {
	PasswordResetRequired bool     `json:"passwordResetRequired,omitempty"`
	AuthUsername          string   `json:"authUsername,omitempty"`
	RememberedLogin       string   `json:"rememberedLogin,omitempty"`
	NeedsVerification     bool     `json:"needsVerification,omitempty"`
	DistributionID        string   `json:"distributionId"`
	Lease                 Envelope `json:"lease"`
	HighestTime           int64    `json:"highestTime"`
	FailedLogins          int      `json:"failedLogins"`
	RetryLoginAt          int64    `json:"retryLoginAt"`
	NextAutoAttempt       int64    `json:"nextAutoAttempt"`
	RetryNetworkAt        int64    `json:"retryNetworkAt"`
	Locked                string   `json:"locked"`
	Event                 *Event   `json:"event,omitempty"`
}

type Status struct {
	Phase                        string `json:"phase"`
	Message                      string `json:"message"`
	Edition                      string `json:"edition"`
	Version                      string `json:"version"`
	DistributionID               string `json:"distributionId,omitempty"`
	Username                     string `json:"username,omitempty"`
	LicenseKey                   string `json:"licenseKey,omitempty"`
	CheckedAt                    int64  `json:"checkedAt,omitempty"`
	RemainingSeconds             int64  `json:"remainingSeconds,omitempty"`
	ExpiresAt                    int64  `json:"expiresAt,omitempty"`
	RefreshAt                    int64  `json:"refreshAt,omitempty"`
	RetryLoginAt                 int64  `json:"retryLoginAt,omitempty"`
	RetryNetworkAt               int64  `json:"retryNetworkAt,omitempty"`
	RetryNetworkRemainingSeconds int64  `json:"retryNetworkRemainingSeconds,omitempty"`
	RetryLoginRemainingSeconds   int64  `json:"retryLoginRemainingSeconds,omitempty"`
	Ready                        bool   `json:"ready"`
}

type Config struct {
	Root                  string
	Storage               string
	Edition               string
	Version               string
	ServerURL             string
	DistributionPublicKey string
	ServerPublicKey       string
	ActivatePath          string
	VerifyPath            string
	EventPath             string
	Client                *http.Client
	Device                func() (string, error)
	DeviceInfo            func() DeviceInfo
	Protect               func([]byte) ([]byte, error)
	Unprotect             func([]byte) ([]byte, error)
	Now                   func() time.Time
	OnLock                func()
}

// decodeKey 解码并校验 Ed25519 公钥。
func decodeKey(text string) (ed25519.PublicKey, error) {
	key, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("管理员尚未配置有效的授权公钥")
	}
	return ed25519.PublicKey(key), nil
}

// verifyEnvelope 验证签名信封，并将载荷解码到目标对象。
func verifyEnvelope(value Envelope, key ed25519.PublicKey, target any) error {
	body, err := base64.RawURLEncoding.DecodeString(value.Payload)
	if err != nil || len(body) > 256<<10 {
		return errors.New("授权数据格式无效")
	}
	signature, err := base64.RawURLEncoding.DecodeString(value.Signature)
	if err != nil || !ed25519.Verify(key, body, signature) {
		return errors.New("授权签名校验失败")
	}
	return json.Unmarshal(body, target)
}

// randomID 生成授权协议使用的随机请求标识。
func randomID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// validRelative 检查文件名是否为允许的相对路径。
func validRelative(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return filepath.ToSlash(filepath.Clean(name)) == name
}

// hashReader 读取输入流并计算 SHA-256 摘要。
func hashReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
