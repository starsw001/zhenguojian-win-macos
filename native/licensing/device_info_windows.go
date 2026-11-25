package licensing

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformDeviceInfo() DeviceInfo {
	version := windows.RtlGetVersion()
	info := DeviceInfo{OS: windowsSystemName(version.MajorVersion, version.MinorVersion, version.BuildNumber, version.ProductType)}
	size := uint32(15 * 1024)
	flags := uint32(windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_DNS_SERVER | windows.GAA_FLAG_INCLUDE_GATEWAYS)
	for attempt := 0; attempt < 3 && size <= 1<<20; attempt++ {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return info
		}
		var best *windows.IpAdapterAddresses
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.OperStatus != windows.IfOperStatusUp || adapter.FirstUnicastAddress == nil || adapter.PhysicalAddressLength != 6 || adapter.IfType != windows.IF_TYPE_ETHERNET_CSMACD && adapter.IfType != windows.IF_TYPE_IEEE80211 {
				continue
			}
			mac := formatDeviceMAC(adapter.PhysicalAddress[:6])
			if mac == "" {
				continue
			}
			gateway := adapter.FirstGatewayAddress != nil
			if best == nil || gateway && best.FirstGatewayAddress == nil || gateway == (best.FirstGatewayAddress != nil) && (adapter.Ipv4Metric < best.Ipv4Metric || adapter.Ipv4Metric == best.Ipv4Metric && adapter.IfIndex < best.IfIndex) {
				best, info.MAC = adapter, mac
			}
		}
		runtime.KeepAlive(buffer)
		return info
	}
	return info
}
