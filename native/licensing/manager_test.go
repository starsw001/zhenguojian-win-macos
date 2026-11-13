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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

type transportFunc func(*http.Request) (*http.Response, error)

// RoundTrip 实现隔离测试使用的单次 HTTP 请求处理。
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type licenseFixture struct {
	config          Config
	now             time.Time
	server          ed25519.PrivateKey
	distributor     ed25519.PrivateKey
	posts           int
	events          int
	wrongNonce      bool
	revoked         bool
	status          int
	expiry          int64
	activated       int64
	inputs          []map[string]any
	leaseUsername   string
	leaseStatus     string
	leaseMonths     int
	renewalRevision int64
	termStartedAt   int64
	licenseKey      string
	advanceOnPost   func()
}

// signed 返回signed。
func signed(t *testing.T, key ed25519.PrivateKey, payload any) Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{Payload: base64.RawURLEncoding.EncodeToString(data), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, data))}
}

// newFixture 创建Fixture。
func newFixture(t *testing.T) *licenseFixture {
	t.Helper()
	f := &licenseFixture{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), status: http.StatusOK, leaseUsername: "fixture1@example.invalid", leaseMonths: 1, leaseStatus: "active"}
	serverPublic, serverPrivate, _ := ed25519.GenerateKey(rand.Reader)
	distributionPublic, distributionPrivate, _ := ed25519.GenerateKey(rand.Reader)
	f.server, f.distributor = serverPrivate, distributionPrivate
	root, storage := t.TempDir(), t.TempDir()
	files := map[string]string{}
	for _, name := range []string{"hongguoshang.exe", "duanju_core.dll", "data/app.so"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := []byte("neutral-runtime-" + name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		files[name] = hex.EncodeToString(hash[:])
	}
	salt := []byte("neutral-test-salt-not-a-real-user")
	d := Distribution{Category: 1, Protocol: Protocol, DistributionID: "fixture-distribution", ReleaseID: "fixture-release", Edition: "hongguoshang", Version: "1.1.1+132", Username: "fixture1@example.invalid", Salt: base64.RawURLEncoding.EncodeToString(salt), Iterations: 200000, Months: 1, Files: files}
	d.PasswordHash = base64.RawURLEncoding.EncodeToString(pbkdf2.Key([]byte("Abc123@#"), salt, d.Iterations, 32, sha256.New))
	d.Permit = signed(t, f.distributor, ActivationPermit{Category: 1, Protocol: Protocol, Kind: "activation-permit", DistributionID: d.DistributionID, Edition: d.Edition, Months: d.Months, Username: d.Username})
	manifest, _ := json.Marshal(signed(t, f.distributor, d))
	if err := os.WriteFile(filepath.Join(root, "distribution.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	f.expiry = f.now.AddDate(0, 1, 0).Unix()
	f.activated = f.now.Unix()
	f.config = Config{Root: root, Storage: storage, Edition: "hongguoshang", Version: "1.1.1+132", ServerURL: "https://license-server.star-andy-jack.workers.dev", DistributionPublicKey: base64.RawURLEncoding.EncodeToString(distributionPublic), ServerPublicKey: base64.RawURLEncoding.EncodeToString(serverPublic), ActivatePath: "/api/v1/activate", VerifyPath: "/api/v1/verify", EventPath: "/api/v1/events", Now: func() time.Time { return f.now }, Device: func() (string, error) { return strings.Repeat("a", 64), nil }, Protect: func(v []byte) ([]byte, error) { return append([]byte("neutral-store:"), v...), nil }, Unprotect: func(v []byte) ([]byte, error) { return v[len("neutral-store:"):], nil }}
	f.config.Client = &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		f.posts++
		var input map[string]any
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		f.inputs = append(f.inputs, input)
		var result any
		if request.URL.Path == f.config.EventPath {
			f.events++
			result = map[string]any{"accepted": true, "eventId": input["eventId"]}
		} else {
			nonce, _ := input["nonce"].(string)
			if f.wrongNonce {
				nonce = "wrong-nonce"
			}
			status := f.leaseStatus
			if f.revoked {
				status = "revoked"
			}
			refresh := f.now.Add(24 * time.Hour).Unix()
			if refresh > f.expiry {
				refresh = f.expiry
			}
			key := f.licenseKey
			if status != "active" {
				key = ""
			}
			result = signed(t, f.server, Lease{LicenseEdition: "hongguoshang", Category: 1, Protocol: Protocol, LicenseID: "fixture-license", DistributionID: "fixture-distribution", Edition: "hongguoshang", DeviceHash: strings.Repeat("a", 64), Nonce: nonce, Status: status, IssuedAt: f.now.Unix(), ActivatedAt: f.activated, ExpiresAt: f.expiry, RefreshAt: refresh, Months: f.leaseMonths, Username: f.leaseUsername, LicenseKey: key, RenewalRevision: f.renewalRevision, TermStartedAt: f.termStartedAt})
		}
		if f.advanceOnPost != nil {
			f.advanceOnPost()
		}
		body, _ := json.Marshal(result)
		return &http.Response{StatusCode: f.status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	return f
}

// TestOfflineLoginNeverActivatesOrContactsServer 验证「Offline Login Never Activates Or Contacts Server」回归场景。
func TestOfflineLoginNeverActivatesOrContactsServer(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	if s := m.Poll(true); s.Phase != "login" || s.Ready || s.Message != "请输入您的专属用户名和密码" {
		t.Fatalf("startup=%+v", s)
	}
	if s := m.Login("fixture1@example.invalid", "wrong"); s.Ready || s.Phase != "login" {
		t.Fatalf("wrong=%+v", s)
	}
	if s := m.Login("fixture1@example.invalid", "Abc123@#"); s.Phase != "activation" || s.Ready {
		t.Fatalf("login=%+v", s)
	}
	if f.posts != 0 || m.Allowed() {
		t.Fatal("offline login granted a license or used network")
	}
}

// TestActivationDailyLeaseRestartAndOneMachine 验证「Activation Daily Lease Restart And One Machine」回归场景。
func TestActivationDailyLeaseRestartAndOneMachine(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := m.Activate("TEST-VALID"); !s.Ready {
		t.Fatalf("activate=%+v", s)
	}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(time.Hour)
		if !m.Poll(true).Ready {
			t.Fatal("valid cached lease rejected")
		}
	}
	if f.posts != 1 {
		t.Fatalf("daily requests=%d", f.posts)
	}
	restarted := Open(f.config)
	if status := restarted.Poll(true); !status.Ready || !restarted.Allowed() || f.posts != 1 {
		t.Fatalf("same-machine cache should remember login without a request: %+v", status)
	}
	other := f.config
	other.Device = func() (string, error) { return strings.Repeat("b", 64), nil }
	cloned := Open(other)
	if cloned.Login("fixture1@example.invalid", "Abc123@#").Ready || cloned.Allowed() {
		t.Fatal("copied state authorized another machine")
	}
	f.now = f.now.Add(24 * time.Hour)
	if !restarted.Poll(true).Ready || f.posts != 2 {
		t.Fatal("daily refresh failed")
	}
	if restarted.state.Lease.Payload == "" {
		t.Fatal("no signed proof persisted")
	}
}

// TestRestartReusesLeaseAndRevocationAppliesAtDailyCheck 验证「Restart Reuses Lease And Revocation Applies At Daily Check」回归场景。
func TestRestartReusesLeaseAndRevocationAppliesAtDailyCheck(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("fixture activation failed")
	}
	f.revoked = true
	restarted := Open(f.config)
	if s := restarted.Poll(true); !s.Ready || !restarted.Allowed() || f.posts != 1 {
		t.Fatalf("valid daily cache should not be rechecked on restart: %+v", s)
	}
	f.now = f.now.Add(24 * time.Hour)
	if s := restarted.Poll(true); s.Ready || s.Phase != "revoked" || !strings.Contains(s.Message, "已停用") || restarted.Allowed() || f.posts != 2 {
		t.Fatalf("signed revocation was not applied before access: %+v", s)
	}
	revoked, err := restarted.readLease(restarted.state.Lease, "")
	if err != nil || revoked.Status != "revoked" {
		t.Fatal("signed revocation was not preserved")
	}
	f.revoked = false
	if s := restarted.Verify(); !s.Ready || !restarted.Allowed() || f.posts != 3 {
		t.Fatalf("online re-enable should not require entering the key again: %+v", s)
	}
}

// TestRestartUsesValidOfflineCacheAndFailsClosedWhenDailyCheckIsDue 验证「Restart Uses Valid Offline Cache And Fails Closed When Daily Check Is Due」回归场景。
func TestRestartUsesValidOfflineCacheAndFailsClosedWhenDailyCheckIsDue(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("fixture activation failed")
	}
	saved := m.state.Lease
	client := f.config.Client
	attempts := 0
	f.config.Client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("fixture offline")
	})}
	restarted := Open(f.config)
	if s := restarted.Poll(true); !s.Ready || attempts != 0 {
		t.Fatalf("valid cached restart: %+v", s)
	}
	f.now = f.now.Add(24 * time.Hour)
	if s := restarted.Poll(true); s.Ready || s.Phase != "verify" || !strings.Contains(s.Message, "无法连接") || restarted.Allowed() || attempts != 1 || restarted.state.Lease != saved {
		t.Fatalf("offline startup unlocked or lost the saved lease: %+v", s)
	}
	f.now = f.now.Add(time.Minute)
	restarted.config.Client = client
	if s := restarted.Verify(); !s.Ready || !restarted.Allowed() || f.posts != 2 {
		t.Fatalf("explicit retry should restore an enabled license after network recovery: %+v", s)
	}
}

// TestRestartDoesNotBypassServerRetryAfter 验证「Restart Does Not Bypass Server Retry After」回归场景。
func TestRestartDoesNotBypassServerRetryAfter(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("fixture activation failed")
	}
	f.now = f.now.Add(24 * time.Hour)
	f.status = http.StatusTooManyRequests
	restarted := Open(f.config)
	restarted.Login("fixture1@example.invalid", "Abc123@#")
	if s := restarted.Poll(true); s.Ready || f.posts != 2 || restarted.state.RetryNetworkAt != f.now.Unix()+3600 {
		t.Fatalf("startup did not respect Retry-After: %+v", s)
	}
	again := Open(f.config)
	again.Login("fixture1@example.invalid", "Abc123@#")
	f.now = f.now.Add(59 * time.Minute)
	if again.Poll(true).Ready || again.Verify().Ready || f.posts != 2 {
		t.Fatal("another restart bypassed the stored Retry-After")
	}
	f.now = f.now.Add(time.Minute)
	f.status = http.StatusOK
	if s := again.Verify(); !s.Ready || f.posts != 3 || again.state.RetryNetworkAt != 0 {
		t.Fatalf("startup was not rechecked after cooldown: %+v", s)
	}
}

// TestSignatureNonceExpiryAndClockRollbackFailClosed 验证「Signature Nonce Expiry And Clock Rollback Fail Closed」回归场景。
func TestSignatureNonceExpiryAndClockRollbackFailClosed(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	f.wrongNonce = true
	if m.Activate("TEST-VALID").Ready {
		t.Fatal("wrong nonce accepted")
	}
	f.now = f.now.Add(2 * time.Minute)
	f.wrongNonce = false
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("valid activation rejected")
	}
	f.now = f.now.Add(-10 * time.Minute)
	if m.Allowed() || m.Poll(false).Ready {
		t.Fatal("clock rollback accepted")
	}
	f.now = time.Unix(f.expiry+1, 0)
	if m.Allowed() {
		t.Fatal("expired license accepted")
	}
	bad := m.state.Lease
	bad.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	if _, err := m.readLease(bad, ""); err == nil {
		t.Fatal("forged signature accepted")
	}
}

// TestTamperLocksOnlyLicenseStateAndReportsOneLogicalEvent 验证「Tamper Locks Only License State And Reports One Logical Event」回归场景。
func TestTamperLocksOnlyLicenseStateAndReportsOneLogicalEvent(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activation")
	}
	keep := filepath.Join(t.TempDir(), "kept.mp4")
	original := []byte("user video fixture must remain untouched")
	os.WriteFile(keep, original, 0600)
	path := filepath.Join(f.config.Root, "duanju_core.dll")
	os.WriteFile(path, []byte("modified-runtime"), 0600)
	os.Chtimes(path, f.now.Add(time.Hour), f.now.Add(time.Hour))
	if s := m.Poll(true); s.Phase != "locked" || s.Ready {
		t.Fatalf("tamper=%+v", s)
	}
	if m.state.Lease.Payload != "" || m.state.Event == nil || !m.state.Event.Reported {
		t.Fatal("active authorization cache or event state incorrect")
	}
	m.Poll(true)
	m.Poll(true)
	if f.events != 1 {
		t.Fatalf("event duplicates=%d", f.events)
	}
	data, _ := os.ReadFile(keep)
	if string(data) != string(original) {
		t.Fatal("user file changed")
	}
	for _, input := range f.inputs {
		if _, ok := input["password"]; ok {
			t.Fatal("password sent to server")
		}
		if input["nonce"] != nil && input["username"] != "fixture1@example.invalid" {
			t.Fatal("dedicated username missing from activation or daily verification")
		}
	}
	restart := Open(f.config)
	if restart.Login("fixture1@example.invalid", "Abc123@#").Ready {
		t.Fatal("lock did not persist")
	}
}

// TestUnavailableServiceAndRetryAfterDoNotExtendLease 验证「Unavailable Service And Retry After Do Not Extend Lease」回归场景。
func TestUnavailableServiceAndRetryAfterDoNotExtendLease(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activate")
	}
	old := m.state.Lease
	f.now = f.now.Add(25 * time.Hour)
	f.status = 429
	if m.Poll(true).Ready {
		t.Fatal("lease extended after failed validation")
	}
	if m.state.Lease != old || m.state.RetryNetworkAt < f.now.Unix()+3600 {
		t.Fatal("lease or retry-after changed incorrectly")
	}
	n := f.posts
	m.Poll(true)
	m.Verify()
	if f.posts != n {
		t.Fatal("retry-after ignored")
	}
}

// TestInvalidPackageAndExcessMonthsNeverUnlock 验证「Invalid Package And Excess Months Never Unlock」回归场景。
func TestInvalidPackageAndExcessMonthsNeverUnlock(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(filepath.Join(f.config.Root, "injected.dll"), []byte("unlisted"), 0600)
	m := Open(f.config)
	if m.Poll(false).Phase != "locked" || m.Allowed() {
		t.Fatal("unlisted module accepted")
	}
	if validRelative("../data/app.so") || validRelative("C:/data/app.so") || validRelative("data\\app.so") {
		t.Fatal("unsafe path accepted")
	}
	f = newFixture(t)
	m = Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	lease := Lease{LicenseEdition: "hongguoshang", Category: 1, Protocol: Protocol, LicenseID: "fixture-license", DistributionID: "fixture-distribution", Edition: "hongguoshang", DeviceHash: strings.Repeat("a", 64), Nonce: "n", Status: "active", IssuedAt: f.now.Unix(), ActivatedAt: f.activated, ExpiresAt: f.now.AddDate(2, 0, 0).Unix(), RefreshAt: f.now.Add(time.Hour).Unix(), Months: 13}
	if _, err := m.readLease(signed(t, f.server, lease), "n"); err == nil {
		t.Fatal("over12month lease accepted")
	}
}

// TestCalendarMonthCapAndRefreshCannotRestartValidity 验证「Calendar Month Cap And Refresh Cannot Restart Validity」回归场景。
func TestCalendarMonthCapAndRefreshCannotRestartValidity(t *testing.T) {
	start := time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC)
	if got := addLicenseMonths(start, 1); got != time.Date(2026, 2, 28, 8, 0, 0, 0, time.UTC) {
		t.Fatalf("calendar month=%s", got)
	}
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activate")
	}
	old := m.state.Lease
	f.now = f.now.Add(25 * time.Hour)
	f.activated = f.now.Unix()
	f.expiry = f.now.AddDate(0, 1, 0).Unix()
	if m.Poll(true).Ready || m.state.Lease != old {
		t.Fatal("daily refresh restarted validity")
	}
}

// TestExpiredSessionRequiresLoginBeforeSignedRenewal 验证「Expired Session Requires Login Before Signed Renewal」回归场景。
func TestExpiredSessionRequiresLoginBeforeSignedRenewal(t *testing.T) {
	f := newFixture(t)
	f.config.ActivatePath, f.config.VerifyPath = "/api/v1/activate-account", "/api/v1/verify-account"
	f.licenseKey = "AAAA-BBBB-CCCC-DDDD"
	stops := make(chan struct{}, 2)
	f.config.OnLock = func() { stops <- struct{}{} }
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := m.Activate(f.licenseKey); !s.Ready || s.LicenseKey != f.licenseKey || f.inputs[0]["renewalVersion"] != float64(1) {
		t.Fatal("expected signed license and capability")
	}
	oldExpiry, originalActivation := f.expiry, f.activated
	f.now = time.Unix(oldExpiry, 0)
	if s := m.Poll(false); s.Phase != "login" || s.Ready || s.LicenseKey != "" || m.Allowed() {
		t.Fatal("expiration did not clear the authenticated session")
	}
	select {
	case <-stops:
	case <-time.After(time.Second):
		t.Fatal("licensed work was not stopped")
	}
	if s := m.Verify(); s.Phase != "login" || f.posts != 1 {
		t.Fatal("verification bypassed the offline login")
	}
	if s := m.Login("fixture1@example.invalid", "Abc123@#"); s.Phase != "renewal" || s.Ready {
		t.Fatal("missing locked renewal state")
	}
	f.leaseStatus = "expired"
	if s := m.Verify(); s.Phase != "renewal" || s.Ready {
		t.Fatal("an expired response unlocked the application")
	}
	f.now = f.now.Add(time.Minute)
	f.renewalRevision, f.termStartedAt, f.leaseMonths, f.leaseStatus = 1, f.now.Unix(), 2, "active"
	f.expiry = addLicenseMonths(f.now, 2).Unix()
	if s := m.Verify(); !s.Ready || s.ExpiresAt != f.expiry || s.LicenseKey != f.licenseKey {
		t.Fatal("valid signed two-month renewal did not restore access")
	}
	lease, err := m.readLease(m.state.Lease, "")
	if err != nil || lease.ActivatedAt != originalActivation || lease.RenewalRevision != 1 || lease.TermStartedAt != f.termStartedAt {
		t.Fatal("first activation was not preserved")
	}
	restarted := Open(f.config)
	if !restarted.Poll(true).Ready || f.posts != 3 {
		t.Fatal("valid renewed cache should remember login and the daily request time")
	}
}

// TestCrossingOldExpiryDuringRenewalDoesNotSkipRelogin 验证「Crossing Old Expiry During Renewal Does Not Skip Relogin」回归场景。
func TestCrossingOldExpiryDuringRenewalDoesNotSkipRelogin(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activation failed")
	}
	oldExpiry := f.expiry
	f.now = time.Unix(oldExpiry-1, 0)
	f.renewalRevision, f.termStartedAt, f.leaseMonths = 1, oldExpiry, 2
	f.expiry = addLicenseMonths(time.Unix(oldExpiry, 0), 2).Unix()
	f.advanceOnPost = func() { f.now = time.Unix(oldExpiry, 0) }
	if s := m.Verify(); s.Phase != "login" || s.Ready || m.Allowed() {
		t.Fatal("response crossed expiry without requiring login")
	}
	if s := m.Login("fixture1@example.invalid", "Abc123@#"); !s.Ready || s.ExpiresAt != f.expiry {
		t.Fatal("signed renewal was not retained for the fresh login")
	}
}

// TestRenewalRejectsWrongAnchorExcessHorizonAndOldRevision 验证「Renewal Rejects Wrong Anchor Excess Horizon And Old Revision」回归场景。
func TestRenewalRejectsWrongAnchorExcessHorizonAndOldRevision(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activation failed")
	}
	oldExpiry := f.expiry
	f.now = f.now.Add(25 * time.Hour)
	f.renewalRevision, f.termStartedAt, f.leaseMonths = 1, oldExpiry-1, 2
	f.expiry = addLicenseMonths(time.Unix(oldExpiry-1, 0), 2).Unix()
	if m.Verify().Ready {
		t.Fatal("overlapping signed term was accepted")
	}
	f.now = f.now.Add(time.Minute)
	f.termStartedAt, f.leaseMonths = oldExpiry, 12
	f.expiry = addLicenseMonths(time.Unix(oldExpiry, 0), 12).Unix()
	if m.Verify().Ready {
		t.Fatal("more than 12 future months was accepted")
	}
	f.now = f.now.Add(time.Minute)
	f.leaseMonths = 2
	f.expiry = addLicenseMonths(time.Unix(oldExpiry, 0), 2).Unix()
	if !m.Verify().Ready {
		t.Fatal("valid renewal rejected")
	}
	latest := m.state.Lease
	f.now = f.now.Add(25 * time.Hour)
	f.renewalRevision, f.termStartedAt, f.leaseMonths, f.expiry = 0, 0, 1, oldExpiry
	if m.Verify().Ready || m.state.Lease != latest {
		t.Fatal("a previous signed term replaced the newer one")
	}
}
