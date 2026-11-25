package licensing

import (
	"regexp"
	"strings"
	"testing"
)

func TestActivationDeviceInfoWindowsCollector(t *testing.T) {
	info := platformDeviceInfo()
	if !strings.HasPrefix(info.OS, "Windows ") || len(info.OS) > 96 {
		t.Fatal("Windows version collection failed")
	}
	if info.MAC != "" && !regexp.MustCompile(`^(?:[0-9A-F]{2}-){5}[0-9A-F]{2}$`).MatchString(info.MAC) {
		t.Fatal("invalid native adapter format")
	}
	t.Log("Windows system version collected; optional adapter MAC validated without printing hardware identifiers")
}
