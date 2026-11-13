package licensing

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"golang.org/x/crypto/pbkdf2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// activateRememberedFixture 激活RememberedFixture。
func activateRememberedFixture(t *testing.T) (*licenseFixture, *Manager) {
	t.Helper()
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activation failed")
	}
	return f, m
}

// TestRememberedLoginNeedsSuccessfulPasswordAndSignedActivation 验证「Remembered Login Needs Successful Password And Signed Activation」回归场景。
func TestRememberedLoginNeedsSuccessfulPasswordAndSignedActivation(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := Open(f.config).Poll(true); s.Phase != "login" || f.posts != 0 {
		t.Fatal("unactivated restart")
	}
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activate")
	}
	body, err := os.ReadFile(filepath.Join(m.config.Storage, "license-state.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Abc123@#") {
		t.Fatal("plaintext password persisted")
	}
	m.state.RememberedLogin = ""
	if err := m.save(); err != nil {
		t.Fatal(err)
	}
	legacy := Open(f.config)
	if s := legacy.Poll(true); s.Phase != "login" || f.posts != 1 {
		t.Fatal("legacy cache must request one password login")
	}
	if !legacy.Login("fixture1@example.invalid", "Abc123@#").Ready {
		t.Fatal("legacy signed cache rejected after password")
	}
	if !Open(f.config).Poll(true).Ready || f.posts != 1 {
		t.Fatal("migration should not repeat activation")
	}
}

// TestRememberedLoginBindingSurvivesUpgradeButNotAccountOrPasswordChange 验证「Remembered Login Binding Survives Upgrade But Not Account Or Password Change」回归场景。
func TestRememberedLoginBindingSurvivesUpgradeButNotAccountOrPasswordChange(t *testing.T) {
	f, m := activateRememberedFixture(t)
	d := m.distribution
	d.Version = "1.1.21+152"
	d.ReleaseID = "upgraded-fixture"
	f.config.Version = d.Version
	write := func() {
		b, _ := json.Marshal(signed(t, f.distributor, d))
		if err := os.WriteFile(filepath.Join(f.config.Root, "distribution.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if !Open(f.config).Poll(true).Ready || f.posts != 1 {
		t.Fatal("same account update forgot login")
	}
	salt, _ := base64.RawURLEncoding.DecodeString(d.Salt)
	d.PasswordHash = base64.RawURLEncoding.EncodeToString(pbkdf2.Key([]byte("Def456@#"), salt, d.Iterations, 32, sha256.New))
	write()
	changed := Open(f.config)
	if changed.Poll(true).Phase != "login" || changed.Login(d.Username, "Abc123@#").Ready {
		t.Fatal("old password authorized changed distribution")
	}
	if !changed.Login(d.Username, "Def456@#").Ready {
		t.Fatal("new password failed")
	}
	d.Username = "different@fixture.com"
	d.Permit = signed(t, f.distributor, ActivationPermit{Category: 1, Protocol: Protocol, Kind: "activation-permit", DistributionID: d.DistributionID, Edition: d.Edition, Months: d.Months, Username: d.Username})
	write()
	if s := Open(f.config).Poll(true); s.Phase != "login" || s.Ready {
		t.Fatal("account change reused login")
	}
}

// TestRememberedDailyBoundaryDoesNotSlideOnRestart 验证「Remembered Daily Boundary Does Not Slide On Restart」回归场景。
func TestRememberedDailyBoundaryDoesNotSlideOnRestart(t *testing.T) {
	f, _ := activateRememberedFixture(t)
	original := f.now
	for _, delay := range []time.Duration{time.Minute, 12 * time.Hour, 24*time.Hour - time.Second} {
		f.now = original.Add(delay)
		m := Open(f.config)
		if !m.Poll(true).Ready || f.posts != 1 {
			t.Fatal("cache unexpectedly refreshed")
		}
	}
	f.now = original.Add(24 * time.Hour)
	m := Open(f.config)
	if !m.Poll(true).Ready || f.posts != 2 {
		t.Fatal("daily boundary did not refresh")
	}
	if !Open(f.config).Poll(true).Ready || f.posts != 2 {
		t.Fatal("restart retried successful check")
	}
}

// TestRememberedLoginDoesNotRestoreExpiredOrRevokedLease 验证「Remembered Login Does Not Restore Expired Or Revoked Lease」回归场景。
func TestRememberedLoginDoesNotRestoreExpiredOrRevokedLease(t *testing.T) {
	f, m := activateRememberedFixture(t)
	f.now = time.Unix(f.expiry, 0)
	if s := Open(f.config).Poll(true); s.Phase != "login" || s.Ready || f.posts != 1 {
		t.Fatal("expired cache restored")
	}
	if m.Poll(true).Phase != "login" || m.Allowed() {
		t.Fatal("running session did not expire")
	}
	f2, m2 := activateRememberedFixture(t)
	f2.now = f2.now.Add(24 * time.Hour)
	f2.revoked = true
	if m2.Poll(true).Phase != "revoked" {
		t.Fatal("revocation not received")
	}
	if s := Open(f2.config).Poll(true); s.Phase != "login" || s.Ready {
		t.Fatal("revoked cache restored")
	}
}

// TestRememberedFailureBudgetSharedAcrossRestartsAndManagers 验证「Remembered Failure Budget Shared Across Restarts And Managers」回归场景。
func TestRememberedFailureBudgetSharedAcrossRestartsAndManagers(t *testing.T) {
	f, _ := activateRememberedFixture(t)
	first, stale := Open(f.config), Open(f.config)
	f.now = f.now.Add(24 * time.Hour)
	f.status = 503
	if first.Poll(true).Ready || f.posts != 2 {
		t.Fatal("due failure")
	}
	f.now = f.now.Add(2 * time.Hour)
	for i := 0; i < 4; i++ {
		if Open(f.config).Poll(true).Ready {
			t.Fatal("failed check restored cache")
		}
	}
	if stale.Poll(true).Ready || f.posts != 2 {
		t.Fatal("restart or stale process bypassed daily failure budget")
	}
	f.status = 200
	if !stale.Verify().Ready || f.posts != 3 {
		t.Fatal("explicit recovery failed")
	}
	if !first.Poll(true).Ready || f.posts != 3 {
		t.Fatal("other process did not reuse recovered lease")
	}
}

// TestRememberedConcurrentManagersMakeOnlyOneDailyRequest 验证「Remembered Concurrent Managers Make Only One Daily Request」回归场景。
func TestRememberedConcurrentManagersMakeOnlyOneDailyRequest(t *testing.T) {
	f, _ := activateRememberedFixture(t)
	managers := []*Manager{Open(f.config), Open(f.config), Open(f.config), Open(f.config)}
	f.now = f.now.Add(24 * time.Hour)
	var wg sync.WaitGroup
	results := make(chan Status, len(managers))
	for _, m := range managers {
		wg.Add(1)
		go func(m *Manager) { defer wg.Done(); results <- m.Poll(true) }(m)
	}
	wg.Wait()
	close(results)
	for s := range results {
		if !s.Ready {
			t.Fatalf("concurrent result %+v", s)
		}
	}
	if f.posts != 2 {
		t.Fatalf("duplicate daily checks: %d", f.posts)
	}
}

// TestRememberedRevocationStopsWorkAndRetainsOtherFiles 验证「Remembered Revocation Stops Work And Retains Other Files」回归场景。
func TestRememberedRevocationStopsWorkAndRetainsOtherFiles(t *testing.T) {
	f := newFixture(t)
	stops := make(chan struct{}, 4)
	f.config.OnLock = func() { stops <- struct{}{} }
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	m.Activate("TEST-VALID")
	keep := filepath.Join(m.config.Storage, "unrelated-record.json")
	os.WriteFile(keep, []byte("preserve"), 0600)
	f.now = f.now.Add(24 * time.Hour)
	f.revoked = true
	if m.Poll(true).Ready {
		t.Fatal("revoked")
	}
	select {
	case <-stops:
	case <-time.After(time.Second):
		t.Fatal("licensed work not stopped")
	}
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != "preserve" {
		t.Fatal("unrelated data changed")
	}
}

// TestRememberedRealPlatformProtection 验证「Remembered Real Platform Protection」回归场景。
func TestRememberedRealPlatformProtection(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI integration")
	}
	f := newFixture(t)
	f.config.Protect = nil
	f.config.Unprotect = nil
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("platform protected activation failed")
	}
	if !Open(f.config).Poll(true).Ready || f.posts != 1 {
		t.Fatal("platform protected restart failed")
	}
	body, err := os.ReadFile(filepath.Join(m.config.Storage, "license-state.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Abc123@#") || strings.Contains(string(body), m.state.RememberedLogin) {
		t.Fatal("protected credentials exposed")
	}
}

// TestLicenseStateLockAcrossProcesses 验证「License State Lock Across Processes」回归场景。
func TestLicenseStateLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("GUO_LICENSE_LOCK_CHILD"); path != "" {
		unlock, err := lockLicenseState(path)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if err := os.WriteFile(path+".acquired", []byte("locked"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "state.lock")
	unlock, err := lockLicenseState(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLicenseStateLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "GUO_LICENSE_LOCK_CHILD="+path)
	if err := cmd.Start(); err != nil {
		unlock()
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	_, statErr := os.Stat(path + ".acquired")
	unlock()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if statErr == nil {
		t.Fatal("child did not wait for parent lock")
	}
	if _, err := os.Stat(path + ".acquired"); err != nil {
		t.Fatal("child failed to acquire released lock")
	}
}

// TestRememberedSessionExpiresWhileDailyNetworkCheckFails 验证「Remembered Session Expires While Daily Network Check Fails」回归场景。
func TestRememberedSessionExpiresWhileDailyNetworkCheckFails(t *testing.T) {
	f, _ := activateRememberedFixture(t)
	f.now = time.Unix(f.expiry-2, 0)
	f.status = 503
	m := Open(f.config)
	if s := m.Poll(true); s.Ready || s.Phase != "verify" {
		t.Fatal("unverified cache")
	}
	f.now = time.Unix(f.expiry, 0)
	if s := m.Poll(true); s.Ready || s.Phase != "login" || m.Allowed() {
		t.Fatal("waiting daily verification bypassed expiry logout")
	}
	if f.posts != 2 {
		t.Fatal("expiry triggered extra network request")
	}
}

// TestDisabledHTTPResponseBlocksUseWithoutRestartRetry 验证「Disabled HTTP Response Blocks Use Without Restart Retry」回归场景。
func TestDisabledHTTPResponseBlocksUseWithoutRestartRetry(t *testing.T) {
	f, _ := activateRememberedFixture(t)
	f.now = f.now.Add(24 * time.Hour)
	f.status = 403
	m := Open(f.config)
	if m.Poll(true).Ready || f.posts != 2 {
		t.Fatal("disabled service response accepted")
	}
	if Open(f.config).Poll(true).Ready || f.posts != 2 {
		t.Fatal("restart bypassed rejection or retried")
	}
}
