package core

import (
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoMobileV5CarrierVectors(t *testing.T) {
	source, _ := hex.DecodeString("13385d82a7ccf1163b6085aacff4193e6388add2f71c41668bb0d5fa1f44698eb3d8fd22476c91b6db00254a6f94b9de03284d7297bce1062b50759abfe4092e53789dc2e70c31567ba0c5ea0f34597ea3c8ed12375c81a6cbf0153a5f84a9cef3183d6287acd1f61b40658aafd4f91e43688db2d7fc21466b90b5daff24496e93b8dd02274c7196bbe0052a4f7499bee3082d52779cc1e60b30557a9fc4e90e33587da2c7ec11365b80a5caef14395e83a8cdf2173c6186abd0f51a3f6489aed3f81d42678cb1d6fb20456a8fb4d9fe23486d92b7dc01264b7095badf04294e7398bde2072c51769bc0e50a2f54799ec3e80d32577ca1c6eb10355a7fa4c9ee13385d82a7ccf1163b6085aacff4193e6388add2f71c41668bb0d5fa1f44698eb3d8fd22476c91b6db00254a")
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	iv, _ := hex.DecodeString("101112131415161718191a1b1c1d1e1f")
	vectors := []struct {
		timestamp uint32
		digest    string
	}{
		{1800000000, "c937a8c291bffb0cc6501646c5acf07c4238ca97e8abdea94dbfe638cb17f6ea"},
		{1800000001, "7729a372229b539339ab432fe65972a1c2c476e8712d84a94a0019dd7d3aca10"},
		{1800000002, "b52cf7a9f4b973e4ab21a8633844dd32cc5b874d13ef67ecdfbbeab01fe39e7c"},
		{1800000003, "6832691e9e4bdcf40fb76cd33462a34358814a5c2bdcf571fa4df81cbd8e5894"},
	}
	for _, vector := range vectors {
		actual := newHongguoMobileV5Aes(key, vector.timestamp).encrypt(source, iv)
		digest := sha256.Sum256(actual)
		if fmt.Sprintf("%x", digest) != vector.digest {
			t.Fatalf("timestamp=%d digest=%x", vector.timestamp, digest)
		}
	}
}

func TestHongguoMobileV5F13Vectors(t *testing.T) {
	vectors := []struct {
		timestamp uint32
		query     string
		body      string
		digest    string
	}{
		{1800000000, "97f67a81cea547c71c76702cc529330762c14971d85f3c335467579d1e9c15e3", "0ff85a33ee8c8aa4f59a843d5199909f", "ca03ec7a0c2444b3dcccf33453d02579"},
		{1800000001, "959b0479339d92d4ab2c428cff3daa0349a78072e56115d2fd8181ef00ef722d", "49783eb0095375c17655cdc1ff329874", "a1376d7977abbc1dbc536aae0429ac2c"},
		{1800000002, "37c02962735148636add7f1430394e36013faedcd015f1c5519773153b0efc81", "c9ee9a33be57c4125ee742ee67f68cff", "acadd6dc830f1d3249c77c86d1a03ab7"},
		{1800000003, "1ec50c23a9d19b673a4b7037cc51f20b594f04d37234da13b52321f5fd1c7a18", "fcd833cf4fa23db062ea04a99de1f6f1", "7f18dac9add0b303bc6fdd4bfd181b39"},
	}
	for _, vector := range vectors {
		query, _ := hex.DecodeString(vector.query)
		body, _ := hex.DecodeString(vector.body)
		actual := hongguoMobileV5HashF13(query, body, vector.timestamp)
		if hex.EncodeToString(actual[:16]) != vector.digest || len(actual) != 20 || actual[16]&0x24 != 0x24 {
			t.Fatalf("timestamp=%d digest=%x", vector.timestamp, actual)
		}
	}
}

func TestHongguoMobileV5MedusaFieldVector(t *testing.T) {
	original := hongguoMultiRandomUint32
	originalNonce := hongguoMultiProcessNonce
	originalStarted := hongguoMultiProcessStarted
	values := []uint32{0x11223344, 0x55667788, 0x99aabbcc}
	index := 0
	hongguoMultiProcessNonce = "00112233445566778899aabbccddeeff0011"
	hongguoMultiProcessStarted = 1700000000000
	hongguoMultiRandomUint32 = func() uint32 {
		value := values[index]
		index++
		return value
	}
	defer func() {
		hongguoMultiRandomUint32 = original
		hongguoMultiProcessNonce = originalNonce
		hongguoMultiProcessStarted = originalStarted
	}()
	actual := hongguoMobileV5Medusa(
		"https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1?aid=8662&version_name=7.4.3.32&device_id=123456789",
		[]byte("field-vector"),
		1800000000,
		map[string]string{"device_id": "123456789", "version_name": "7.4.3.32"},
	)
	digest := sha256.Sum256([]byte(actual))
	if got, want := fmt.Sprintf("%x", digest), "dd0b13227cb39330b803278dcc2b28c0adb45bb15cc9d8ce0c210bc8dfd4c9ab"; got != want {
		t.Fatalf("digest=%s want=%s", got, want)
	}
}

func TestHongguoMobileV5FetchesRequestedEpisodeAndAllAddresses(t *testing.T) {
	const videoID = "5678"
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host == "log5-applog-lf.fqnovel.com" {
			return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		}
		if request.URL.Host != "api5-normal-sinfonlinea.fqnovel.com" || request.URL.Path != "/novel/player/multi_video_model/player/v1" {
			t.Fatalf("unexpected endpoint: %s", request.URL)
		}
		if request.Header.Get("X-Medusa") == "" || request.Header.Get("X-Helios") == "" || request.Header.Get("X-Khronos") == "" || request.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("missing v5 headers: %#v", request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		if request.Header.Get("X-SS-STUB") != fmt.Sprintf("%X", md5.Sum(body)) {
			t.Fatal("compressed payload digest mismatch")
		}
		reader, err := gzip.NewReader(strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := io.ReadAll(reader)
		reader.Close()
		var payload map[string]any
		if json.Unmarshal(plain, &payload) != nil || nestedMap(payload, "mixed_video_id_map")["1004"] == nil {
			t.Fatalf("wrong payload: %s", plain)
		}
		response := `{"Code":0,"data":{"1004":{"5678":{"video_model":{"video_duration":12.5,"video_list":[{"main_url":"https://one.qznovelvod.com/5678.mp4","backup_url_list":["https://two.fqnovelvod.com/5678.mp4"],"video_meta":{"codec_type":"h264","definition":"720p"},"encrypt_info":{"encrypt":false}}]}}}}}`
		return sourceFixtureResponse(request, http.StatusOK, response), nil
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), videoID)
	if err != nil || calls != 2 || media.URL != "https://one.qznovelvod.com/5678.mp4" || len(media.Variants) != 2 || media.Duration != 12500*time.Millisecond {
		t.Fatalf("calls=%d media=%+v err=%v", calls, media, err)
	}
}

func TestHongguoMobileV5RegistersFreshIdentityForEveryEpisode(t *testing.T) {
	registrations, reads := 0, 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Hostname() {
		case "log5-applog-lf.fqnovel.com":
			registrations++
			return sourceFixtureResponse(request, http.StatusOK, fmt.Sprintf(`{"code":0,"device_id_str":"12345678%d","install_id_str":"98765432%d"}`, registrations, registrations)), nil
		case "api5-normal-sinfonlinea.fqnovel.com":
			reads++
			videoID := "5678"
			if reads == 2 {
				videoID = "6789"
			}
			return sourceFixtureResponse(request, http.StatusOK, fmt.Sprintf(`{"Code":0,"data":{"1004":{"%s":{"video_model":{"video_list":[{"main_url":"https://one.qznovelvod.com/%s.mp4","video_meta":{"codec_type":"h264","definition":"720p"},"encrypt_info":{"encrypt":false}}]}}}}}`, videoID, videoID)), nil
		default:
			t.Fatalf("unexpected endpoint: %s", request.URL)
			return nil, nil
		}
	})
	first, firstErr := d.resolveHongguoMobileV5Media(context.Background(), "5678")
	second, secondErr := d.resolveHongguoMobileV5Media(context.Background(), "6789")
	if firstErr != nil || secondErr != nil || registrations != 2 || reads != 2 || !strings.Contains(first.URL, "5678") || !strings.Contains(second.URL, "6789") {
		t.Fatalf("registrations=%d reads=%d first=%+v firstErr=%v second=%+v secondErr=%v", registrations, reads, first, firstErr, second, secondErr)
	}
}

func TestHongguoMobileV5RecoversEmptyPostWithSameEpisodeTabRoutes(t *testing.T) {
	const videoID = "5678"
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if request.URL.Host != "log5-applog-lf.fqnovel.com" {
				t.Fatalf("unexpected registration endpoint: %s", request.URL)
			}
			return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		case 2:
			if request.Method != http.MethodPost || request.URL.Host != "api5-normal-sinfonlinea.fqnovel.com" {
				t.Fatalf("unexpected primary endpoint: %s %s", request.Method, request.URL)
			}
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		case 3:
			if request.Method != http.MethodGet || request.URL.Host != "api5-normal-sinfonlineb.fqnovel.com" || request.URL.Path != "/reading/bookapi/video_tab/multi_video_model/v1/" || request.URL.Query().Get("video_id") != videoID {
				t.Fatalf("unexpected recovery endpoint: %s %s", request.Method, request.URL)
			}
			if request.Header.Get("X-Medusa") == "" || request.Header.Get("X-Helios") == "" || request.Header.Get("X-Khronos") == "" || request.Header.Get("X-SS-STUB") != "" || request.Header.Get("Content-Encoding") != "" {
				t.Fatalf("wrong recovery headers: %#v", request.Header)
			}
			response := `{"Code":0,"data":{"video_model_data":{"5678":{"video_model":{"video_duration":8.5,"video_list":{"720p":{"main_url":"https://one.qznovelvod.com/5678.mp4","backup_url_list":["https://two.fqnovelvod.com/5678.mp4"],"video_meta":{"codec_type":"h264","definition":"720p"},"encrypt_info":{"encrypt":false}}}}}}}}`
			return sourceFixtureResponse(request, http.StatusOK, response), nil
		default:
			t.Fatalf("unexpected request %d: %s", calls, request.URL)
			return nil, nil
		}
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), videoID)
	if err != nil || calls != 3 || media.URL != "https://one.qznovelvod.com/5678.mp4" || len(media.Variants) != 2 || media.Duration != 8500*time.Millisecond {
		t.Fatalf("calls=%d media=%+v err=%v", calls, media, err)
	}
}

func TestHongguoMobileV5RejectsOtherEpisodeTabResponse(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		switch (calls - 1) % 3 {
		case 0:
			return sourceFixtureResponse(request, http.StatusOK, fmt.Sprintf(`{"code":0,"device_id_str":"12345678%d","install_id_str":"98765432%d"}`, calls, calls)), nil
		case 1:
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		case 2:
			return sourceFixtureResponse(request, http.StatusOK, `{"Code":0,"data":{"video_model_data":{"9999":{"video_model":{"video_list":{"720p":{"main_url":"https://other.qznovelvod.com/9999.mp4","video_meta":{"codec_type":"h264","definition":"720p"}}}}}}}}`), nil
		}
		return nil, fmt.Errorf("unexpected request %d", calls)
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), "5678")
	if calls != 3 || media.URL != "" || err == nil || !strings.Contains(err.Error(), "未返回所选分集") {
		t.Fatalf("calls=%d media=%+v err=%v", calls, media, err)
	}
}

func TestHongguoMobileV5EmptyBodiesDoNotReplaceDevice(t *testing.T) {
	registrations, primary, recovery := 0, 0, 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Hostname() {
		case "log5-applog-lf.fqnovel.com":
			registrations++
			return sourceFixtureResponse(request, http.StatusOK, fmt.Sprintf(`{"code":0,"device_id_str":"12345678%d","install_id_str":"98765432%d"}`, registrations, registrations)), nil
		case "api5-normal-sinfonlinea.fqnovel.com":
			primary++
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		case "api5-normal-sinfonlineb.fqnovel.com":
			recovery++
			return sourceFixtureResponse(request, http.StatusOK, ""), nil
		default:
			t.Fatalf("unexpected endpoint: %s", request.URL)
			return nil, nil
		}
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), "5678")
	if !errors.Is(err, errHongguoEmptyPrimaryPlayback) || media.URL != "" || registrations != 1 || primary != 1 || recovery != 1 {
		t.Fatalf("registrations=%d primary=%d recovery=%d media=%+v err=%v", registrations, primary, recovery, media, err)
	}
}

func TestHongguoMobileV5DoesNotRotateProtectedResponses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			registrations, primary := 0, 0
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Hostname() == "log5-applog-lf.fqnovel.com" {
					registrations++
					return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
				}
				primary++
				return sourceFixtureResponse(request, status, "protected"), nil
			})
			if _, err := d.resolveHongguoMobileV5Media(context.Background(), "5678"); err == nil || registrations != 1 || primary != 1 {
				t.Fatalf("status=%d registrations=%d primary=%d err=%v", status, registrations, primary, err)
			}
		})
	}
}

func TestMergeHongguoMediaRoutesPreservesAllResolvers(t *testing.T) {
	mobile := providerMedia{URL: "https://mobile.qznovelvod.com/5678.mp4", Referer: "https://mobile.test/", Direct: true, Quality: 720}
	mobile.Variants = []providerMedia{mobile, {URL: "https://shared.qznovelvod.com/5678.mp4", Referer: "https://mobile.test/", Direct: true, Quality: 720}}
	single := providerMedia{URL: "https://shared.qznovelvod.com/5678.mp4", Referer: "https://mobile.test/", Direct: true, Quality: 720}
	single.Variants = []providerMedia{single, {URL: "https://single.fqnovelvod.com/5678.mp4", Direct: true, Quality: 720}}
	multi := providerMedia{URL: "https://multi.qznovelvod.com/5678.mp4", Direct: true, Quality: 720}
	merged := mergeHongguoMediaRoutes([]providerMedia{mobile, single, multi})
	choice := nativePlaybackChoicesForSource(sourceHongguo, merged, 0)
	want := []string{
		"https://mobile.qznovelvod.com/5678.mp4",
		"https://shared.qznovelvod.com/5678.mp4",
		"https://single.fqnovelvod.com/5678.mp4",
		"https://multi.qznovelvod.com/5678.mp4",
	}
	if len(choice.media) != len(want) {
		t.Fatalf("routes=%d media=%+v", len(choice.media), choice.media)
	}
	for index, address := range want {
		if choice.media[index].URL != address {
			t.Fatalf("route %d=%s want=%s", index, choice.media[index].URL, address)
		}
	}
}
