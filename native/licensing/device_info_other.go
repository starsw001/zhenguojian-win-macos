//go:build !windows

package licensing

import "runtime"

func platformDeviceInfo() DeviceInfo {
	return DeviceInfo{OS: runtime.GOOS}
}
