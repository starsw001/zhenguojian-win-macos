package licensing

import (
	"bytes"
	"testing"
)

// TestWindowsAuthorizationStateUsesDPAPI 验证「Windows Authorization State Uses DPAPI」回归场景。
func TestWindowsAuthorizationStateUsesDPAPI(t *testing.T) {
	plain := []byte("neutral authorization state fixture")
	encrypted, err := platformProtect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, plain) {
		t.Fatal("state stored as plaintext")
	}
	decoded, err := platformUnprotect(encrypted)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("DPAPI roundtrip failed")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := platformUnprotect(encrypted); err == nil {
		t.Fatal("corrupt state decrypted")
	}
}
