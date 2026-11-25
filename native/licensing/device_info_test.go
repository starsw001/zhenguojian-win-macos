package licensing

import (
	"strings"
	"testing"
	"time"
)

func TestActivationDeviceInfoVersionLabels(t *testing.T) {
	for _, test := range []struct {
		major, minor, build uint32
		productType         byte
		prefix              string
	}{
		{6, 1, 7601, 1, "Windows 7 "},
		{6, 3, 9600, 1, "Windows 8.1 "},
		{10, 0, 19045, 1, "Windows 10 "},
		{10, 0, 22000, 1, "Windows 11 "},
		{10, 0, 26100, 1, "Windows 11 "},
		{10, 0, 26100, 3, "Windows Server "},
	} {
		if !strings.HasPrefix(windowsSystemName(test.major, test.minor, test.build, test.productType), test.prefix) {
			t.Fatal("incorrect Windows system label", test.prefix)
		}
	}
}

func TestActivationDeviceInfoMACFormatting(t *testing.T) {
	if formatDeviceMAC([]byte{0xc0, 0x02, 0x6c, 0x16, 0xd7, 0x19}) != "C0-02-6C-16-D7-19" {
		t.Fatal("MAC formatting differs from the administrator display format")
	}
	for _, value := range [][]byte{nil, {0, 0, 0, 0, 0, 0}, {1, 2, 3, 4, 5, 6}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, {0xc0, 2}} {
		if formatDeviceMAC(value) != "" {
			t.Fatal("invalid MAC address accepted")
		}
	}
}

func TestActivationDeviceInfoOnlySentDuringExplicitActivation(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.config.DeviceInfo = func() DeviceInfo {
		calls++
		return DeviceInfo{OS: "Windows 11 (Build 26100)", MAC: "C0-02-6C-16-D7-19"}
	}
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("metadata changed the activation result")
	}
	info, ok := f.inputs[0]["deviceInfo"].(map[string]any)
	if !ok || info["os"] != "Windows 11 (Build 26100)" || info["mac"] != "C0-02-6C-16-D7-19" || info["ip"] != nil || calls != 1 || f.inputs[0]["deviceHash"] != strings.Repeat("a", 64) {
		t.Fatal("activation metadata contract or device binding changed")
	}
	f.now = f.now.Add(24 * time.Hour)
	if !m.Poll(true).Ready || calls != 1 || f.inputs[len(f.inputs)-1]["deviceInfo"] != nil {
		t.Fatal("passive verification collected activation metadata")
	}
}

func TestActivationDeviceInfoUnavailableDoesNotBlockActivation(t *testing.T) {
	f := newFixture(t)
	f.config.DeviceInfo = func() DeviceInfo { return DeviceInfo{} }
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if !m.Activate("TEST-VALID").Ready {
		t.Fatal("missing optional metadata blocked activation")
	}
}
