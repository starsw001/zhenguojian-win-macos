package licensing

import (
	"fmt"
	"strings"
)

type DeviceInfo struct {
	OS  string `json:"os,omitempty"`
	MAC string `json:"mac,omitempty"`
}

func windowsSystemName(major, minor, build uint32, productType byte) string {
	name := fmt.Sprintf("Windows %d.%d", major, minor)
	if productType != 1 {
		name = "Windows Server"
	} else if major == 10 {
		name = "Windows 10"
		if build >= 22000 {
			name = "Windows 11"
		}
	} else if major == 6 {
		switch minor {
		case 0:
			name = "Windows Vista"
		case 1:
			name = "Windows 7"
		case 2:
			name = "Windows 8"
		case 3:
			name = "Windows 8.1"
		}
	}
	return fmt.Sprintf("%s (Build %d)", name, build)
}

func formatDeviceMAC(value []byte) string {
	if len(value) != 6 || value[0]&1 != 0 {
		return ""
	}
	parts := make([]string, 6)
	nonzero := false
	for i, b := range value {
		nonzero = nonzero || b != 0
		parts[i] = fmt.Sprintf("%02X", b)
	}
	if !nonzero {
		return ""
	}
	return strings.Join(parts, "-")
}
