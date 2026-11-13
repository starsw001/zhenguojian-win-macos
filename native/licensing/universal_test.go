package licensing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/pbkdf2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUniversalAccountsAndRememberedSession 验证「Universal Accounts And Remembered Session」回归场景。
func TestUniversalAccountsAndRememberedSession(t *testing.T) {
	f := newFixture(t)
	raw, _ := os.ReadFile(filepath.Join(f.config.Root, "distribution.json"))
	var e Envelope
	_ = json.Unmarshal(raw, &e)
	var d map[string]any
	// Decode the already signed neutral fixture without requiring added production struct fields.
	data, _ := base64.RawURLEncoding.DecodeString(e.Payload)
	_ = json.Unmarshal(data, &d)
	d["accountMode"] = "universal"
	d["username"] = ""
	d["passwordHash"] = ""
	d["salt"] = ""
	d["iterations"] = 0
	d["months"] = 12
	d["permit"] = signed(t, f.distributor, map[string]any{"category": 1, "protocol": Protocol, "kind": "activation-permit", "accountMode": "universal", "distributionId": d["distributionId"], "edition": d["edition"], "months": 12})
	raw, _ = json.Marshal(signed(t, f.distributor, d))
	_ = os.WriteFile(filepath.Join(f.config.Root, "distribution.json"), raw, 0600)
	m := Open(f.config)
	if m.Poll(false).Phase == "configuration" {
		fmt.Println(`UNIVERSAL153={"universalManifestAccepted":false,"accountsActivated":0,"rememberedRestarts":0}`)
		t.Fatal("universal manifest rejected")
		return
	}
	previous := f.config.Client.Transport
	salt := []byte(strings.Repeat("s", 24))
	if len(salt) != 24 {
		t.Fatal("salt")
	}
	verifier := pbkdf2.Key([]byte("Abc123@#"), salt, 310000, 32, sha256.New)
	f.config.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "account-challenge") && !strings.HasSuffix(req.URL.Path, "account-login") {
			return previous.RoundTrip(req)
		}
		var in map[string]any
		_ = json.NewDecoder(req.Body).Decode(&in)
		if in["password"] != nil || in["passwordHash"] != nil {
			t.Fatal("credential transmitted")
		}
		code := 200
		var out any
		if strings.HasSuffix(req.URL.Path, "account-challenge") {
			out = map[string]any{"challengeId": strings.Repeat("c", 32), "salt": base64.RawURLEncoding.EncodeToString(salt), "iterations": 310000, "expiresAt": f.now.Unix() + 120}
		} else {
			body, _ := json.Marshal([]any{strings.Repeat("c", 32), in["username"], in["distributionId"], in["edition"], in["deviceHash"], in["nonce"]})
			mac := hmac.New(sha256.New, verifier)
			_, _ = mac.Write(body)
			if in["proof"] != base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) {
				code = 403
				out = map[string]any{"code": "invalid_login"}
			} else {
				out = signed(t, f.server, map[string]any{"protocol": Protocol, "kind": "account-session", "credentialSalt": base64.RawURLEncoding.EncodeToString(salt), "username": in["username"], "distributionId": in["distributionId"], "edition": in["edition"], "deviceHash": in["deviceHash"], "nonce": in["nonce"], "issuedAt": f.now.Unix(), "expiresAt": f.now.Unix() + 600})
			}
		}
		b, _ := json.Marshal(out)
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	activated, restarts := 0, 0
	for _, name := range []string{"alice123@fixture.com", "bob45678@fixture.com"} {
		cfg := f.config
		cfg.Storage = t.TempDir()
		f.leaseUsername = name
		m = Open(cfg)
		if x := m.Login(name, "wrong123"); x.Phase != "login" {
			t.Fatal("wrong password accepted")
		}
		if x := m.Login(name, "Abc123@#"); x.Phase != "activation" {
			t.Fatalf("login %s %s", x.Phase, x.Message)
		}
		if x := m.Activate("ABCD-EFGH-JKLM-NPQR"); !x.Ready || x.Username != name {
			t.Fatalf("activate %s %s", x.Phase, x.Message)
		}
		activated++
		if x := Open(cfg).Poll(false); !x.Ready || x.Username != name {
			t.Fatalf("restart %s %s", x.Phase, x.Message)
		}
		restarts++
	}
	fmt.Printf("UNIVERSAL153={\"universalManifestAccepted\":true,\"accountsActivated\":%d,\"rememberedRestarts\":%d}\n", activated, restarts)
}
