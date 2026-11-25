package core

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHongguoProxyModeDefaultsAndValidation(t *testing.T) {
	defaults := defaultResourceSettings()
	if defaults.HongguoProxyMode != "auto" || defaults.validate() != nil {
		t.Fatal("default Hongguo route is not direct-first", defaults.HongguoProxyMode)
	}
	var legacy nativeResourceSettings
	for _, data := range []string{`{"proxyMode":"direct"}`, `{"proxyMode":"direct","hongguoProxyMode":"system"}`, `{"proxyMode":"direct","hongguoProxyMode":"direct"}`} {
		if err := json.Unmarshal([]byte(data), &legacy); err != nil || legacy.HongguoProxyMode != "auto" || legacy.ProxyMode != "direct" {
			t.Fatal("legacy Hongguo migration changed unrelated settings", legacy, err)
		}
	}
	for _, mode := range []string{"auto", "directOnly"} {
		settings := defaults
		settings.HongguoProxyMode = mode
		if err := settings.validate(); err != nil {
			t.Fatal("valid Hongguo route rejected", mode, err)
		}
	}
	settings := defaults
	settings.HongguoProxyMode = "manual"
	if settings.validate() == nil {
		t.Fatal("unsupported Hongguo route accepted")
	}
}

func TestHongguoProxyModeIsHostScoped(t *testing.T) {
	router := autoProxyFixtureRouter("auto")
	router.hongguoMode = "directOnly"
	for _, address := range []string{
		"https://api5-normal-sinfonlineb.fqnovel.com/reading/reader/full/v/",
		"https://v26-reading-videocdn302.qznovelvod.com/video.mp4",
		"https://sub.hongguoduanju.com/catalog",
	} {
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		mode, _ := router.requestMode(request)
		proxy, err := router.proxy(request)
		if mode != "direct" || proxy != nil || err != nil {
			t.Fatal("Hongguo request did not stay direct", address, mode, proxy, err)
		}
	}
	for _, address := range []string{
		"https://qznovelvod.com.example.invalid/video.mp4",
		"https://fakeqznovelvod.com/video.mp4",
		"https://example.invalid/catalog",
	} {
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		mode, _ := router.requestMode(request)
		proxy, err := router.proxy(request)
		if mode != "auto" || proxy == nil || err != nil {
			t.Fatal("unrelated request inherited Hongguo mode", address, mode, proxy, err)
		}
	}
}
