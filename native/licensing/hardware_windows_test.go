package licensing

import "testing"

// TestWindowsMachineFingerprintStable 验证「Windows Machine Fingerprint Stable」回归场景。
func TestWindowsMachineFingerprintStable(t *testing.T) {
	first, err := machineHash()
	if err != nil {
		t.Fatal(err)
	}
	second, err := machineHash()
	if err != nil || len(first) != 64 || first != second {
		t.Fatal("Windows machine fingerprint is not stable")
	}
}
