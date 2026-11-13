package licensing

import (
	"testing"
	"time"
)

// TestSignedLeaseMustMatchDedicatedUsername 验证「Signed Lease Must Match Dedicated Username」回归场景。
func TestSignedLeaseMustMatchDedicatedUsername(t *testing.T) {
	for _, name := range []string{"", "another@example.invalid", "Fixture1@example.invalid"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.leaseUsername = name
			m := Open(f.config)
			m.Login("fixture1@example.invalid", "Abc123@#")
			if s := m.Activate("TEST-VALID"); s.Ready || m.Allowed() || m.state.Lease.Payload != "" {
				t.Fatal("mismatched signed account granted authorization")
			}
		})
	}
}

// TestAccountExpiryStatusAndExactDeadline 验证「Account Expiry Status And Exact Deadline」回归场景。
func TestAccountExpiryStatusAndExactDeadline(t *testing.T) {
	f := newFixture(t)
	f.expiry = f.now.Add(3*time.Hour + 7*time.Second).Unix()
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	s := m.Activate("TEST-VALID")
	if !s.Ready || s.Username != "fixture1@example.invalid" || s.RemainingSeconds != 10807 || s.ExpiresAt != f.expiry {
		t.Fatalf("expiry status=%+v", s)
	}
	if _, ok := f.inputs[0]["passwordHash"]; ok {
		t.Fatal("password verifier uploaded")
	}
	f.now = time.Unix(f.expiry, 0)
	if s := m.Poll(false); s.Ready || s.RemainingSeconds != 0 || m.Allowed() {
		t.Fatalf("exact expiry did not deny=%+v", s)
	}
}

// TestUsernameBoundDailyLeaseCannotSwitchAccountOrExtendExpiry 验证「Username Bound Daily Lease Cannot Switch Account Or Extend Expiry」回归场景。
func TestUsernameBoundDailyLeaseCannotSwitchAccountOrExtendExpiry(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("activation failed")
	}
	f.now = f.now.Add(24*time.Hour + time.Second)
	f.leaseUsername = "another@example.invalid"
	if m.Poll(true).Ready {
		t.Fatal("wrong daily username accepted")
	}
	f.now = f.now.Add(24*time.Hour + time.Second)
	f.leaseUsername = "fixture1@example.invalid"
	f.expiry += 60
	if m.Poll(true).Ready {
		t.Fatal("changed daily deadline accepted")
	}
}
