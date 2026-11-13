package licensing

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPasswordResetSignedCheckLogsOutAndPersistsAcrossProcesses 验证「Password Reset Signed Check Logs Out And Persists Across Processes」回归场景。
func TestPasswordResetSignedCheckLogsOutAndPersistsAcrossProcesses(t *testing.T) {
	f := newFixture(t)
	f.licenseKey = "ABCD-EFGH-JKLM-NPQR"
	m := Open(f.config)
	m.Login(f.leaseUsername, "Abc123@#")
	if !m.Activate(f.licenseKey).Ready {
		t.Fatal("activation")
	}
	peer := Open(f.config)
	if !peer.Poll(false).Ready {
		t.Fatal("remembered peer")
	}
	prior := m.state.Lease
	sentinel := filepath.Join(f.config.Storage, "watch-history-sentinel.json")
	if err := os.WriteFile(sentinel, []byte("do-not-change"), 0600); err != nil {
		t.Fatal(err)
	}
	f.leaseStatus = "password_reset"
	f.now = f.now.Add(24 * time.Hour)
	got := m.Poll(true)
	if got.Phase != "login" || got.Ready || !strings.Contains(got.Message, "密码已被重置") {
		t.Fatalf("reset %+v", got)
	}
	if m.state.RememberedLogin != "" || m.accountSession.Payload != "" || !m.state.PasswordResetRequired || m.state.Lease != prior {
		t.Fatal("session clearing or lease preservation")
	}
	posts := f.posts
	for _, other := range []*Manager{peer, Open(f.config)} {
		if s := other.Poll(true); s.Phase != "login" || s.Ready {
			t.Fatalf("stale process restored %+v", s)
		}
	}
	if f.posts != posts {
		t.Fatal("logged-out polling used network")
	}
	b, _ := os.ReadFile(sentinel)
	if string(b) != "do-not-change" {
		t.Fatal("history touched")
	}
}

// TestPasswordResetRequiresValidSignatureAndNonce 验证「Password Reset Requires Valid Signature And Nonce」回归场景。
func TestPasswordResetRequiresValidSignatureAndNonce(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login(f.leaseUsername, "Abc123@#")
	m.Activate("ABCD-EFGH-JKLM-NPQR")
	f.now = f.now.Add(24 * time.Hour)
	f.leaseStatus = "password_reset"
	f.wrongNonce = true
	got := m.Poll(true)
	if got.Phase == "login" || m.state.PasswordResetRequired {
		t.Fatal("forged reset accepted")
	}
	if got.Ready {
		t.Fatal("bad signature refresh unlocked")
	}
}

// TestPasswordResetSaveFailureFailsClosed 验证「Password Reset Save Failure Fails Closed」回归场景。
func TestPasswordResetSaveFailureFailsClosed(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login(f.leaseUsername, "Abc123@#")
	m.Activate("ABCD-EFGH-JKLM-NPQR")
	m.config.Protect = func([]byte) ([]byte, error) { return nil, os.ErrPermission }
	m.requirePasswordLogin()
	if s := m.status(); s.Ready || s.Phase != "configuration" {
		t.Fatalf("save failure %+v", s)
	}
}

// TestPasswordResetCannotRestoreByEditingRememberedToken 验证「Password Reset Cannot Restore By Editing Remembered Token」回归场景。
func TestPasswordResetCannotRestoreByEditingRememberedToken(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login(f.leaseUsername, "Abc123@#")
	m.Activate("ABCD-EFGH-JKLM-NPQR")
	m.requirePasswordLogin()
	m.state.RememberedLogin = m.loginBinding()
	if err := m.save(); err != nil {
		t.Fatal(err)
	}
	if s := Open(f.config).Poll(false); s.Ready || s.Phase != "login" {
		t.Fatalf("restored %+v", s)
	}
}

// TestUniversalFreshLoginNeverTrustsPreviousRememberedLease 验证「Universal Fresh Login Never Trusts Previous Remembered Lease」回归场景。
func TestUniversalFreshLoginNeverTrustsPreviousRememberedLease(t *testing.T) {
	f := newFixture(t)
	raw, _ := os.ReadFile(filepath.Join(f.config.Root, "distribution.json"))
	var e Envelope
	json.Unmarshal(raw, &e)
	var d map[string]any
	plain, _ := base64.RawURLEncoding.DecodeString(e.Payload)
	json.Unmarshal(plain, &d)
	d["accountMode"] = "universal"
	d["username"] = ""
	d["passwordHash"] = ""
	d["salt"] = ""
	d["iterations"] = 0
	d["months"] = 12
	d["permit"] = signed(t, f.distributor, map[string]any{"category": 1, "protocol": Protocol, "kind": "activation-permit", "accountMode": "universal", "distributionId": d["distributionId"], "edition": d["edition"], "months": 12})
	raw, _ = json.Marshal(signed(t, f.distributor, d))
	os.WriteFile(filepath.Join(f.config.Root, "distribution.json"), raw, 0600)
	m := Open(f.config)
	m.state.AuthUsername = "alice123@fixture.com"
	f.leaseUsername = m.state.AuthUsername
	m.loggedIn = true
	f.licenseKey = "ABCD-EFGH-JKLM-NPQR"
	m.save()
	m.Activate(f.licenseKey)
	m.requirePasswordLogin()
	if s := m.Login("alice123@fixture.com", "wrong123"); s.Ready || s.Phase != "login" {
		t.Fatal("old session bypass")
	}
}
