package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHuangguoPublicationDataOnly 验证「Huangguo Publication Data Only」回归场景。
func TestHuangguoPublicationDataOnly(t *testing.T) {
	body := `<a href="https://ready.example/">立即使用</a>
<a href="https://ready.example/">重复</a><a href="https://t.me/channel">聊天</a>
<a href="https://installer.example/">APP下载</a><a href="https://ready.example/video/1">剧集</a>
<a href="http://insecure.example/">旧入口</a><a href="https://127.0.0.1/">本机</a>
<script src="/publish.js?20261022"></script><script src="https://foreign.example/publish.js"></script>
<script src="/install.js"></script>`
	got := parseHuangguoPublication(body)
	if len(got) != 1 || got[0] != "https://ready.example" {
		t.Fatalf("unexpected publication roots: %v", got)
	}
	scripts := huangguoPublicationScripts(body, "https://publish.example")
	if len(scripts) != 1 || scripts[0] != "https://publish.example/publish.js?20261022" {
		t.Fatalf("unexpected scripts: %v", scripts)
	}
	js := "var subdomains = [\n 'thu',\n // 'disabled',\n 'pku',\n];\nvar urls = [\n 'one.example/',\n 'two.example/',\n];"
	got = parseHuangguoPublication(js)
	if len(got) != 4 || got[0] != "https://thu.one.example" || got[3] != "https://pku.two.example" {
		t.Fatalf("literal arrays not parsed without executing scripts: %v", got)
	}
	got = parseHuangguoPublication(`<a class="use-btn" href="https://route-one.example">使用线路</a><a href="https://unrelated.example">其他站点</a><a class="use-btn" href="https://route-two.example">使用线路</a>`)
	if len(got) != 2 || got[0] != "https://route-one.example" || got[1] != "https://route-two.example" {
		t.Fatalf("route buttons included unrelated links: %v", got)
	}
}

// mirrorFixtureRecord 返回镜像Fixture记录。
func mirrorFixtureRecord(origin, kind string) huangguoMirrorRecord {
	return huangguoMirrorRecord{Origin: origin, EffectiveOrigin: origin, Kind: kind, State: "ready", Poster: true, PosterWidth: 300, PosterHeight: 400, PosterBytes: 8000, Browse: true, Selection: true, Playback: true,
		Width: 1280, Height: 720, PositionMS: 500, AddedOn: "2026-10-23", AddedFrom: "fixture"}
}

// setMirrorFixtures 设置镜像Fixtures。
func setMirrorFixtures(d *Downloader, records ...huangguoMirrorRecord) *huangguoMirrorRegistry {
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	r.disk.Mirrors = records
	r.lastDiscovery, r.lastRecovery = time.Now(), time.Now()
	r.mu.Unlock()
	return r
}

// TestHuangguoCandidatesRequireMatchingDecodedTypeAndDeduplicateRedirects 验证「Huangguo Candidates Require Matching Decoded Type And Deduplicate Redirects」回归场景。
func TestHuangguoCandidatesRequireMatchingDecodedTypeAndDeduplicateRedirects(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoAIURL = "https://primary.example"
	ready := mirrorFixtureRecord("https://ready.example", sourceHuangguoAI)
	alias := mirrorFixtureRecord("https://alias.example", sourceHuangguoAI)
	alias.EffectiveOrigin = ready.Origin
	pending := mirrorFixtureRecord("https://pending.example", sourceHuangguoAI)
	pending.Playback = false
	failed := mirrorFixtureRecord("https://failed.example", sourceHuangguoAI)
	failed.State = "failed"
	wrong := mirrorFixtureRecord("https://video.example", sourceHuangguoVideo)
	setMirrorFixtures(d, ready, alias, pending, failed, wrong)
	got := d.huangguoCandidates(sourceHuangguoAI, "https://primary.example/detail/1/?page=2")
	if len(got) != 2 || got[1] != "https://ready.example/detail/1/?page=2" {
		t.Fatalf("unverified/wrong type/duplicate route used: %v", got)
	}
}

// TestHuangguoPersistenceRetainsFailureDatesAndUnreadableOriginal 验证「Huangguo Persistence Retains Failure Dates And Unreadable Original」回归场景。
func TestHuangguoPersistenceRetainsFailureDatesAndUnreadableOriginal(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	r := setMirrorFixtures(d, huangguoMirrorRecord{Origin: "https://old.example", Kind: sourceHuangguoAI,
		AddedOn: "2026-10-01", AddedFrom: "old", State: "failed", Message: "HTTP 503", LastError: "HTTP 503"})
	r.mu.Lock()
	r.appendLocked("https://old.example/", "2026-10-23", "new publication")
	r.appendLocked("https://new.example/", "2026-10-23", "new publication")
	err := r.saveLocked()
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reopened := &Downloader{cfg: d.cfg}
	entries, _, _ := reopened.huangguoMirrorStore().snapshot()
	if entries[0].AddedOn != "2026-10-01" || entries[0].LastError != "HTTP 503" || entries[1].Origin != "https://new.example" {
		t.Fatalf("lost retained record: %+v", entries[:2])
	}
	path := filepath.Join(t.TempDir(), "huangguo-mirrors.json")
	pristine := []byte("{unreadable original\n")
	if err := os.WriteFile(path, pristine, 0600); err != nil {
		t.Fatal(err)
	}
	broken := (&Downloader{cfg: Config{dataDir: filepath.Dir(path)}}).huangguoMirrorStore()
	broken.mu.Lock()
	err = broken.saveLocked()
	broken.mu.Unlock()
	body, _ := os.ReadFile(path)
	if err == nil || string(body) != string(pristine) {
		t.Fatal("unreadable original overwritten")
	}
}

// TestHuangguoDiscoveryAppendsWithoutDeletionAndUsesCooldown 验证「Huangguo Discovery Appends Without Deletion And Uses Cooldown」回归场景。
func TestHuangguoDiscoveryAppendsWithoutDeletionAndUsesCooldown(t *testing.T) {
	oldPublications := huangguoPublicationURLs
	huangguoPublicationURLs = []string{"https://publication.example"}
	defer func() { huangguoPublicationURLs = oldPublications }()
	var calls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Host != "publication.example" {
			t.Fatalf("discovery fetched a mirror, image or installer: %s", request.URL)
		}
		if request.URL.Path == "/publish.js" {
			return sourceFixtureResponse(request, 200, "var subdomains = [\n 'new',\n];var urls = [\n 'mirror.example/',\n];"), nil
		}
		return sourceFixtureResponse(request, 200, `<script src="/publish.js"></script>`), nil
	})
	r := setMirrorFixtures(d, huangguoMirrorRecord{Origin: "https://old.example", State: "failed", AddedOn: "2026-10-01", Message: "unreachable"})
	count, err := d.discoverHuangguoMirrors(context.Background(), true)
	if err != nil || count != 1 {
		t.Fatalf("discovery: added=%d err=%v", count, err)
	}
	entries, pubs, _ := r.snapshot()
	if len(entries) != 2 || entries[0].State != "failed" || entries[0].AddedOn != "2026-10-01" || entries[1].State != "pending" || entries[1].AddedFrom != huangguoPublicationURLs[0] || len(pubs) != 1 || pubs[0].State != "ok" {
		t.Fatalf("append/pending/provenance lost: %+v %+v", entries, pubs)
	}
	if count, err = d.discoverHuangguoMirrors(context.Background(), false); count != 0 || err != nil || calls.Load() != 2 {
		t.Fatalf("cooldown did not prevent repeated publication fetch: %d %v calls=%d", count, err, calls.Load())
	}
}

// TestHuangguoFailoverPreservesPathAndDoesNotRotateOnAccessControls 验证「Huangguo Failover Preserves Path And Does Not Rotate On Access Controls」回归场景。
func TestHuangguoFailoverPreservesPathAndDoesNotRotateOnAccessControls(t *testing.T) {
	for _, status := range []int{503, 401, 402, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var fallbackCalls atomic.Int32
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "primary.example" {
					return sourceFixtureResponse(request, status, "temporary failure"), nil
				}
				fallbackCalls.Add(1)
				if request.URL.Host != "ready.example" || request.URL.RequestURI() != "/detail/1/?page=2" {
					t.Fatalf("incorrect fallback: %s", request.URL)
				}
				return sourceFixtureResponse(request, 200, "fixture"), nil
			})
			d.cfg.HuangguoAIURL = "https://primary.example"
			setMirrorFixtures(d, mirrorFixtureRecord("https://primary.example", sourceHuangguoAI), mirrorFixtureRecord("https://ready.example", sourceHuangguoAI))
			body, err := d.fetchHuangguoWithMirrors(context.Background(), sourceHuangguoAI, "https://primary.example/detail/1/?page=2", "")
			if status == 503 {
				if err != nil || body != "fixture" || fallbackCalls.Load() != 1 {
					t.Fatalf("eligible failure did not switch: %q %v calls=%d", body, err, fallbackCalls.Load())
				}
			} else if err == nil || fallbackCalls.Load() != 0 {
				t.Fatalf("access control rotated mirror: status=%d err=%v calls=%d", status, err, fallbackCalls.Load())
			}
		})
	}
}

// TestHuangguoValidationRequiresBrowseSelectionAndActualDecode 验证「Huangguo Validation Requires Browse Selection And Actual Decode」回归场景。
func TestHuangguoValidationRequiresBrowseSelectionAndActualDecode(t *testing.T) {
	for _, decoded := range []bool{false, true} {
		t.Run(map[bool]string{false: "decodeFails", true: "decodePasses"}[decoded], func(t *testing.T) {
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				var body string
				switch {
				case request.URL.Path == "/":
					body = `<div class="hg-drama-card" data-track-id="1"><a href="/detail/1/">Fixture</a></div>`
				case strings.HasPrefix(request.URL.Path, "/api/videos/category/"):
					body = `{"items":[{"id":"1","title":"Fixture"}]}`
				case request.URL.Path == "/detail/1/":
					body = `<h1>Fixture</h1><a href="/video/1-1/">第1集</a><a href="/video/1-2/">第2集</a>`
				case strings.HasPrefix(request.URL.Path, "/video/"):
					body = `<div data-play-src="https://media.example/fixture.mp4"></div>`
				default:
					t.Fatalf("unexpected image, media or other request: %s", request.URL)
				}
				return sourceFixtureResponse(request, 200, body), nil
			})
			d.huangguoDecode = func(_ context.Context, media providerMedia) (huangguoDecodeResult, error) {
				if media.URL != "https://media.example/fixture.mp4" {
					t.Fatalf("incorrect media: %+v", media)
				}
				if !decoded {
					return huangguoDecodeResult{}, errors.New("fixture decoder rejected media")
				}
				return huangguoDecodeResult{Width: 1280, Height: 720, PositionMS: 500}, nil
			}
			d.huangguoPoster = func(context.Context, Drama, string) (huangguoPosterResult, error) {
				return huangguoPosterResult{Width: 300, Height: 400, Bytes: 8000, Message: "fixture decoded poster"}, nil
			}
			result := d.checkHuangguoMirror(context.Background(), "https://fixture.example")
			if result.Kind != sourceHuangguoAI || !result.Browse || !result.Selection || usableHuangguoMirror(result, sourceHuangguoAI) != decoded {
				data, _ := json.Marshal(result)
				t.Fatalf("incorrect activation gate: %s", data)
			}
			if !decoded && (!strings.Contains(result.Message, "实际播放失败") || result.LastError == "") {
				t.Fatalf("failure reason was not retained: %+v", result)
			}
		})
	}
}

// TestHuangguoPublicationRetriesOnlyOneEligibleFailure 验证「Huangguo Publication Retries Only One Eligible Failure」回归场景。
func TestHuangguoPublicationRetriesOnlyOneEligibleFailure(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		var calls atomic.Int32
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			if blocked {
				return sourceFixtureResponse(request, 403, "forbidden"), nil
			}
			return nil, errors.New("fixture connection closed")
		})
		_, err := d.fetchHuangguoPublicationText(context.Background(), "https://publication.example/", "")
		expected := int32(2)
		if blocked {
			expected = 1
		}
		if err == nil || calls.Load() != expected {
			t.Fatalf("unbounded or access-control retry: err=%v calls=%d blocked=%v", err, calls.Load(), blocked)
		}
	}
}

// TestHuangguoFailureDiscoversChecksAndActivatesNewOrigin 验证「Huangguo Failure Discovers Checks And Activates New Origin」回归场景。
func TestHuangguoFailureDiscoversChecksAndActivatesNewOrigin(t *testing.T) {
	oldPublications := huangguoPublicationURLs
	huangguoPublicationURLs = []string{"https://publication.example"}
	defer func() { huangguoPublicationURLs = oldPublications }()
	var decodes atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		body := ""
		if request.URL.Host == "primary.example" {
			return sourceFixtureResponse(request, 503, "unavailable"), nil
		}
		if request.URL.Host == "publication.example" {
			return sourceFixtureResponse(request, 200, `<a href="https://new.example/">立即使用</a>`), nil
		}
		if request.URL.Host != "new.example" {
			t.Fatalf("unexpected destination: %s", request.URL)
		}
		switch {
		case request.URL.Path == "/":
			body = `<div class="hg-drama-card" data-track-id="1"><a href="/detail/1/">Fixture</a></div>`
		case strings.HasPrefix(request.URL.Path, "/api/videos/category/"):
			body = `{"items":[{"id":"1","title":"Fixture"}]}`
		case request.URL.Path == "/detail/1/":
			body = `<h1>Fixture</h1><a href="/video/1-1/">第1集</a>`
		case request.URL.Path == "/video/1-1/":
			body = `<div data-play-src="https://media.example/fixture.mp4"></div>`
		default:
			t.Fatalf("unexpected path: %s", request.URL)
		}
		return sourceFixtureResponse(request, 200, body), nil
	})
	d.cfg.HuangguoAIURL = "https://primary.example"
	r := setMirrorFixtures(d, mirrorFixtureRecord("https://primary.example", sourceHuangguoAI))
	r.lastDiscovery, r.lastRecovery = time.Time{}, time.Time{}
	d.huangguoDecode = func(context.Context, providerMedia) (huangguoDecodeResult, error) {
		decodes.Add(1)
		return huangguoDecodeResult{Width: 1280, Height: 720, PositionMS: 500}, nil
	}
	d.huangguoPoster = func(context.Context, Drama, string) (huangguoPosterResult, error) {
		return huangguoPosterResult{Width: 300, Height: 400, Bytes: 8000, Message: "fixture decoded poster"}, nil
	}
	body, err := d.fetchProviderText(context.Background(), "https://primary.example/detail/1/", "")
	entries, _, _ := r.snapshot()
	if err != nil || !strings.Contains(body, "Fixture") || decodes.Load() != 1 || len(entries) != 2 || entries[0].State != "failed" || !usableHuangguoMirror(entries[1], sourceHuangguoAI) {
		t.Fatalf("recovery failed: err=%v decodes=%d entries=%+v", err, decodes.Load(), entries)
	}
}

// TestHuangguoSeededStatusDoesNotFetchOrReplaceSourceHistory 验证「Huangguo Seeded Status Does Not Fetch Or Replace Source History」回归场景。
func TestHuangguoSeededStatusDoesNotFetchOrReplaceSourceHistory(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("reading mirror status requested the network: %s", request.URL)
		return nil, errors.New("unexpected request")
	})
	engine.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{Error: "prior source failure"}
	status := engine.sourceStatus(sourceHuangguoAI)
	if status.Error != "prior source failure" || len(status.Mirrors) < 27 || len(status.MirrorPublications) != len(huangguoPublicationURLs) {
		t.Fatalf("status lost retained data: mirrors=%d publications=%d error=%q", len(status.Mirrors), len(status.MirrorPublications), status.Error)
	}
	var records []huangguoMirrorRecord
	if err := json.Unmarshal([]byte(huangguoMirrorSeedJSON), &records); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range records {
		_, dateErr := time.Parse("2006-01-02", row.AddedOn)
		if seen[row.Origin] || normalizeHuangguoOrigin(row.Origin) != row.Origin || dateErr != nil || row.AddedFrom == "" || row.Message == "" {
			t.Fatalf("invalid retained seed: %+v", row)
		}
		seen[row.Origin] = true
		unfolded := row
		unfolded.DuplicateOf = ""
		if row.State == "ready" && (!huangguoSource(row.Kind) || !usableHuangguoMirror(unfolded, row.Kind)) {
			t.Fatalf("seed falsely claims decoded success: %+v", row)
		}
		if row.DuplicateOf != "" && usableHuangguoMirror(row, row.Kind) {
			t.Fatalf("duplicate seed enabled separately: %+v", row)
		}
	}
}

// TestHuangguoFailedPosterCannotActivateOrStartPlayback 验证「Huangguo Failed Poster Cannot Activate Or Start Playback」回归场景。
func TestHuangguoFailedPosterCannotActivateOrStartPlayback(t *testing.T) {
	var decodes atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/" {
			return sourceFixtureResponse(request, 200, `<div class="hg-drama-card" data-track-id="1"><a href="/detail/1/">Fixture</a></div>`), nil
		}
		if strings.HasPrefix(request.URL.Path, "/api/videos/category/") {
			return sourceFixtureResponse(request, 200, `{"items":[{"id":"1","title":"Fixture"}]}`), nil
		}
		t.Fatalf("failed poster still fetched detail or playback: %s", request.URL)
		return nil, errors.New("unexpected request")
	})
	d.huangguoPoster = func(context.Context, Drama, string) (huangguoPosterResult, error) {
		return huangguoPosterResult{}, errors.New("fixture real poster decoder failed")
	}
	d.huangguoDecode = func(context.Context, providerMedia) (huangguoDecodeResult, error) {
		decodes.Add(1)
		return huangguoDecodeResult{Width: 1280, Height: 720, PositionMS: 500}, nil
	}
	row := d.checkHuangguoMirror(context.Background(), "https://fixture.example")
	if row.State != "failed" || row.Poster || !row.Browse || row.Playback || decodes.Load() != 0 || !strings.Contains(row.Message, "真实海报失败") {
		t.Fatalf("poster gate was bypassed: %+v decodes=%d", row, decodes.Load())
	}
}

// TestHuangguoFoldedDuplicatesRetainTheirOwnFailureRecords 验证「Huangguo Folded Duplicates Retain Their Own Failure Records」回归场景。
func TestHuangguoFoldedDuplicatesRetainTheirOwnFailureRecords(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoAIURL = "https://canonical.example"
	canonical := mirrorFixtureRecord("https://canonical.example", sourceHuangguoAI)
	alias := mirrorFixtureRecord("https://alias.example", sourceHuangguoAI)
	alias.EffectiveOrigin = canonical.Origin
	failed := alias
	failed.Origin, failed.State, failed.Message, failed.LastError = "https://failed-alias.example", "failed", "fixture EOF", "fixture EOF"
	r := setMirrorFixtures(d, alias, failed, canonical)
	rows, _, _ := r.snapshot()
	if len(rows) != 3 || rows[0].DuplicateOf != canonical.Origin || rows[1].DuplicateOf != canonical.Origin || rows[1].LastError != "fixture EOF" || rows[2].DuplicateOf != "" {
		t.Fatalf("duplicates not folded or failure lost: %+v", rows)
	}
	got := d.huangguoCandidates(sourceHuangguoAI, canonical.Origin+"/detail/1/")
	if len(got) != 1 || got[0] != canonical.Origin+"/detail/1/" || usableHuangguoMirror(rows[0], sourceHuangguoAI) {
		t.Fatalf("alias still used as a separate line: %v", got)
	}
	withoutPoster := canonical
	withoutPoster.Poster = false
	if usableHuangguoMirror(withoutPoster, sourceHuangguoAI) {
		t.Fatal("prior decoder-only record was activated without poster verification")
	}
}

// TestHuangguoPublicationPageIsNotAVideoSource 验证「Huangguo Publication Page Is Not A Video Source」回归场景。
func TestHuangguoPublicationPageIsNotAVideoSource(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `<h1>最新地址发布</h1><a href="https://actual.example/">立即使用</a>`), nil
	})
	row := d.checkHuangguoMirror(context.Background(), "https://publication.example")
	if row.Kind != "publication" || row.State != "failed" || row.Playback || !strings.Contains(row.Message, "直接的黄果AI或视频站根地址") {
		t.Fatalf("publication was mistaken for playback source: %+v", row)
	}
}

// TestHuangguoPosterUsesRealImageBytesAndOriginReferer 验证「Huangguo Poster Uses Real Image Bytes And Origin Referer」回归场景。
func TestHuangguoPosterUsesRealImageBytesAndOriginReferer(t *testing.T) {
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 4))
	img.Set(0, 0, color.RGBA{R: 128, A: 255})
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.URL.String() != "https://image.example/poster.png" || request.Header.Get("Referer") != "https://origin.example/" {
			t.Fatalf("poster source/referer changed: %s %s", request.URL, request.Header.Get("Referer"))
		}
		return sourceFixtureResponse(request, 200, buffer.String()), nil
	})
	decode := func(_ context.Context, input string) (huangguoDecodeResult, error) {
		file, err := os.Open(input)
		if err != nil {
			return huangguoDecodeResult{}, err
		}
		defer file.Close()
		decoded, _, err := image.Decode(file)
		if err != nil {
			return huangguoDecodeResult{}, err
		}
		return huangguoDecodeResult{Width: decoded.Bounds().Dx(), Height: decoded.Bounds().Dy()}, nil
	}
	row, err := engine.checkHuangguoPoster(context.Background(), Drama{ID: "huangguoai:fixture", Source: sourceHuangguoAI, SourceID: "fixture", Cover: "https://image.example/poster.png"}, "https://origin.example", decode)
	if err != nil || row.Width != 3 || row.Height != 4 || row.Bytes != int64(buffer.Len()) || requests.Load() != 1 {
		t.Fatalf("poster load/decode failed: %+v err=%v requests=%d", row, err, requests.Load())
	}
}

// TestHuangguoRootRetriesTransientNetworkFailureButNotAccessControl 验证「Huangguo Root Retries Transient Network Failure But Not Access Control」回归场景。
func TestHuangguoRootRetriesTransientNetworkFailureButNotAccessControl(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		var calls atomic.Int32
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			count := calls.Add(1)
			if blocked {
				return sourceFixtureResponse(request, 403, "forbidden"), nil
			}
			if count == 1 {
				return nil, errors.New("fixture transient EOF")
			}
			return sourceFixtureResponse(request, 200, "fixture root"), nil
		})
		body, _, err := d.fetchHuangguoMirrorRoot(context.Background(), "https://fixture.example")
		if blocked && (err == nil || calls.Load() != 1) || !blocked && (err != nil || body != "fixture root" || calls.Load() != 2) {
			t.Fatalf("incorrect retry: blocked=%t calls=%d error=%v", blocked, calls.Load(), err)
		}
	}
}

// TestHuangguoRecoveryDoesNotRepeatSameRouteOrProbePublicationPage 验证「Huangguo Recovery Does Not Repeat Same Route Or Probe Publication Page」回归场景。
func TestHuangguoRecoveryDoesNotRepeatSameRouteOrProbePublicationPage(t *testing.T) {
	var calls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Host == "publication.example" {
			t.Fatal("publication page used as playback recovery candidate")
		}
		return nil, errors.New("fixture network closed")
	})
	canonical := mirrorFixtureRecord("https://canonical.example", sourceHuangguoAI)
	canonical.State, canonical.RetryAt = "failed", time.Now().Add(time.Minute)
	alias := mirrorFixtureRecord("https://alias-one.example", sourceHuangguoAI)
	alias.EffectiveOrigin = canonical.Origin
	second := alias
	second.Origin = "https://alias-two.example"
	publication := huangguoMirrorRecord{Origin: "https://publication.example", Kind: "publication", State: "failed"}
	r := setMirrorFixtures(d, canonical, alias, second, publication)
	r.lastRecovery = time.Time{}
	d.recoverHuangguoMirrors(context.Background(), sourceHuangguoAI)
	if calls.Load() != 2 {
		t.Fatalf("same effective route was repeatedly checked: requests=%d", calls.Load())
	}
}
