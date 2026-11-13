package licensing

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestActivationFailureExposesSixtySecondWait 验证「Activation Failure Exposes Sixty Second Wait」回归场景。
func TestActivationFailureExposesSixtySecondWait(t *testing.T) {
	f := newFixture(t)
	good := f.config.Client.Transport
	f.config.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		f.posts++
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"license_edition_mismatch"}`))}, nil
	})
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	s := m.Activate("WRONG-EDITION")
	if s.RetryNetworkRemainingSeconds != 60 || s.RetryNetworkAt != f.now.Unix()+60 || !strings.Contains(s.Message, "不适用于当前软件") {
		t.Fatalf("first failure: %+v", s)
	}
	f.now = f.now.Add(59 * time.Second)
	s = m.Activate("CORRECT-EDITION")
	if s.RetryNetworkRemainingSeconds != 1 || !strings.Contains(s.Message, "0 分 01 秒") || f.posts != 1 {
		t.Fatalf("blocked attempt: %+v posts=%d", s, f.posts)
	}
	f.now = f.now.Add(time.Second)
	f.config.Client.Transport = good
	s = m.Activate("CORRECT-EDITION")
	if !s.Ready || s.RetryNetworkRemainingSeconds != 0 || s.RetryNetworkAt != 0 || f.posts != 2 {
		t.Fatalf("wait finished: %+v posts=%d", s, f.posts)
	}
}

// TestRetryAfterReportedAndSurvivesReopen 验证「Retry After Reported And Survives Reopen」回归场景。
func TestRetryAfterReportedAndSurvivesReopen(t *testing.T) {
	for _, header := range []string{"180", "date", "", "invalid"} {
		t.Run(header, func(t *testing.T) {
			f := newFixture(t)
			value, seconds := header, int64(180)
			if header == "date" {
				value = f.now.Add(3 * time.Minute).Format(http.TimeFormat)
			}
			if header == "" || header == "invalid" {
				seconds = 60
			}
			f.config.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				f.posts++
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{value}}, Body: io.NopCloser(strings.NewReader("busy"))}, nil
			})
			m := Open(f.config)
			m.Login("fixture1@example.invalid", "Abc123@#")
			s := m.Activate("RATE-LIMITED")
			if s.RetryNetworkRemainingSeconds != seconds || !strings.Contains(s.Message, "授权请求等待中") {
				t.Fatalf("initial: %+v", s)
			}
			f.now = f.now.Add(time.Second)
			m = Open(f.config)
			m.Login("fixture1@example.invalid", "Abc123@#")
			s = m.Activate("ANOTHER-KEY")
			if s.RetryNetworkRemainingSeconds != seconds-1 || f.posts != 1 {
				t.Fatalf("reopened: %+v posts=%d", s, f.posts)
			}
		})
	}
}

// TestNetworkWaitDoesNotCountAsWrongPassword 验证「Network Wait Does Not Count As Wrong Password」回归场景。
func TestNetworkWaitDoesNotCountAsWrongPassword(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.distribution.AccountMode = "universal"
	m.state.RetryNetworkAt = f.now.Unix() + 180
	for i := 0; i < 8; i++ {
		s := m.loginUniversal("account1@fixture.com", "Abc123@#")
		if s.RetryNetworkRemainingSeconds != 180 || m.state.FailedLogins != 0 || m.state.RetryLoginAt != 0 || f.posts != 0 {
			t.Fatalf("waiting login counted as failure: %+v", s)
		}
	}
}
