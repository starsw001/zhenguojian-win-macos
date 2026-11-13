package licensing

import (
	"net/http"
	"testing"
)

// TestLicenseTransportProxyFallbackAndExclusions 验证「License Transport Proxy Fallback And Exclusions」回归场景。
func TestLicenseTransportProxyFallbackAndExclusions(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8088")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("REQUEST_METHOD", "")
	request, _ := http.NewRequest(http.MethodGet, "https://license-server.star-andy-jack.workers.dev/api/guoapp/v1/metadata", nil)
	client := defaultLicenseClient()
	transport := client.Transport.(*http.Transport)
	proxy, err := transport.Proxy(request)
	if err != nil || proxy == nil || proxy.String() != "http://127.0.0.1:8088" {
		t.Fatal("HTTPS did not reuse the configured HTTP proxy")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS certificate checks were disabled")
	}
	if client.CheckRedirect(request, nil) == nil {
		t.Fatal("redirect protection was lost")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8099")
	proxy, err = defaultLicenseClient().Transport.(*http.Transport).Proxy(request)
	if err != nil || proxy == nil || proxy.Port() != "8099" {
		t.Fatal("explicit HTTPS proxy was overridden")
	}
	t.Setenv("NO_PROXY", "license-server.star-andy-jack.workers.dev")
	proxy, err = defaultLicenseClient().Transport.(*http.Transport).Proxy(request)
	if err != nil || proxy != nil {
		t.Fatal("NO_PROXY was ignored")
	}
}
