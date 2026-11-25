package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHongguoRegisteredPlaybackUsesOwnIdentityAndPersistsIt 验证「Hongguo Registered Playback Uses Own Identity And Persists It」回归场景。
func TestHongguoRegisteredPlaybackUsesOwnIdentityAndPersistsIt(t *testing.T) {
	var calls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Query().Get("device_id") != "123456789" || request.URL.Query().Get("iid") != "987654321" {
			t.Error("registered identity was replaced")
		}
		if request.Header.Get("X-Medusa") != "" || request.Header.Get("X-Gorgon") == "" || request.Header.Get("User-Agent") != hongguoRegisteredPlaybackUserAgent {
			t.Error("playback request is not signed with the current profile")
		}
		return sourceFixtureResponse(request, 200, `{"code":0,"data":{"5678":{"video_model":{"video_list":{"video_1":{"main_url":"https://synthetic-fixture.qznovelvod.com/5678.mp4","vwidth":1080,"vheight":1920,"codec_type":"h264"}}}}}}`), nil
	})
	client := d.hongguoClient()
	client.state.DeviceID, client.state.InstallID, client.state.Registered = "123456789", "987654321", true
	for i := 0; i < 2; i++ {
		media, err := d.resolveHongguoMultiMedia(context.Background(), "5678")
		if err != nil || !strings.HasSuffix(media.URL, "/5678.mp4") {
			t.Fatalf("registered playback failed: %+v %v", media, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected registration or retry")
	}
	body, err := os.ReadFile(filepath.Join(d.cfg.dataDir, "hongguo-playback-device.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved hongguoPlaybackDevice
	if json.Unmarshal(body, &saved) != nil || !saved.valid() || saved.DeviceID != "123456789" {
		t.Fatal("registered playback identity was not preserved")
	}
	reopened := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("identity load used network")
		return nil, errors.New("forbidden")
	})
	reopened.cfg.dataDir = d.cfg.dataDir
	got, err := reopened.hongguoPlaybackDevice(context.Background())
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("identity did not survive restart: %v", err)
	}
}

// TestHongguoFreshPlaybackRegistrationIsSharedAndValidated 验证「Hongguo Fresh Playback Registration Is Shared And Validated」回归场景。
func TestHongguoFreshPlaybackRegistrationIsSharedAndValidated(t *testing.T) {
	var calls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Hostname() != "log5-applog-lf.fqnovel.com" || request.Header.Get("Content-Type") != "application/octet-stream;tt-data=a" {
			t.Error("unexpected registration route")
		}
		return sourceFixtureResponse(request, 200, `{"device_id_str":"123456789","install_id_str":"987654321"}`), nil
	})
	var wait sync.WaitGroup
	for i := 0; i < 6; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			device, err := d.hongguoPlaybackDevice(context.Background())
			if err != nil || !device.valid() {
				t.Errorf("registration failed: %v", err)
			}
		}()
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent calls rotated registered devices")
	}
	for _, device := range []hongguoPlaybackDevice{{Version: 1, DeviceID: "0", InstallID: "123"}, {Version: 1, DeviceID: "123", InstallID: "000"}} {
		if device.valid() {
			t.Fatal("server zero identity accepted")
		}
	}
}

// TestHongguoPlaybackRegistrationEnvelopeRoundTrip 验证「Hongguo Playback Registration Envelope Round Trip」回归场景。
func TestHongguoPlaybackRegistrationEnvelopeRoundTrip(t *testing.T) {
	body, err := hongguoPlaybackRegistrationBody(time.Unix(1700000000, 0), "11111111-2222-3333-4444-555555555555")
	if err != nil || len(body) < 54 || !bytes.Equal(body[:6], mustHex("746305100000")) {
		t.Fatalf("invalid registration envelope: %v", err)
	}
	seed := sha512.Sum512(body[6:38])
	salt := mustHex("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")
	derived := sha512.Sum512(append(seed[:], salt...))
	block, _ := aes.NewCipher(derived[:16])
	plain := append([]byte{}, body[38:]...)
	cipher.NewCBCDecrypter(block, derived[16:32]).CryptBlocks(plain, plain)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil || len(plain) <= 64 {
		t.Fatal("registration envelope padding invalid")
	}
	hash := sha512.Sum512(plain[64:])
	if !bytes.Equal(hash[:], plain[:64]) {
		t.Fatal("registration envelope digest invalid")
	}
	reader, err := gzip.NewReader(bytes.NewReader(plain[64:]))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Contains(decoded, []byte(`"magic_tag":"ss_app_log"`)) || !bytes.Contains(decoded, []byte(`"cdid":"11111111-2222-3333-4444-555555555555"`)) {
		t.Fatal("registration envelope content invalid")
	}
}

// TestHongguoPlaybackAccessFailuresKeepIdentityAcrossRoutes 验证「Hongguo Playback Access Failures Keep Identity Across Routes」回归场景。
func TestHongguoPlaybackAccessFailuresKeepIdentityAcrossRoutes(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusOK} {
		calls := 0
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			calls++
			response := sourceFixtureResponse(request, status, "")
			if status == http.StatusOK {
				response.Header.Set("Cf-Mitigated", "challenge")
			}
			return response, nil
		})
		client := d.hongguoClient()
		client.state.DeviceID, client.state.InstallID, client.state.Registered = "123456789", "987654321", true
		_, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:1234", Index: 1, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}})
		if err == nil || calls != 1 || client.playbackDevice == nil || client.playbackDevice.DeviceID != "123456789" || client.playbackDevice.InstallID != "987654321" {
			t.Fatalf("failed routes changed playback identity: status %d calls %d err %v", status, calls, err)
		}
	}
}

// TestHongguoPlaybackRegistrationFailureAndInvalidSavedIdentityArePreserved 验证「Hongguo Playback Registration Failure And Invalid Saved Identity Are Preserved」回归场景。
func TestHongguoPlaybackRegistrationFailureAndInvalidSavedIdentityArePreserved(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		return sourceFixtureResponse(request, 200, `{"device_id":"0","install_id":"0"}`), nil
	})
	for i := 0; i < 2; i++ {
		if _, err := d.hongguoPlaybackDevice(context.Background()); err == nil {
			t.Fatal("invalid server identity accepted")
		}
	}
	if calls != 1 {
		t.Fatal("failed registration was repeatedly replaced")
	}
	other := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid saved identity caused network registration")
		return nil, errors.New("forbidden")
	})
	path := filepath.Join(other.cfg.dataDir, "hongguo-playback-device.json")
	original := []byte(`{"version":1,"deviceId":"0","installId":"0"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := other.hongguoPlaybackDevice(context.Background()); err == nil {
		t.Fatal("invalid saved identity accepted")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatal("invalid saved identity was overwritten")
	}
}

// TestHongguoPlaybackMetadataDoesNotForwardPostOnRedirect 验证「Hongguo Playback Metadata Does Not Forward Post On Redirect」回归场景。
func TestHongguoPlaybackMetadataDoesNotForwardPostOnRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	d := sourceFixtureDownloader(t, nil)
	d.client = &http.Client{}
	if _, err := d.hongguoMultiFetchJSON(context.Background(), origin.URL, http.MethodPost, nil, []byte(`{"own":"device"}`), nil); err == nil {
		t.Fatal("metadata redirect accepted")
	}
	if forwarded.Load() != 0 {
		t.Fatal("metadata body crossed a redirect")
	}
}
