package core

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSoraniAccountLoginRequiredIsExplicit(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{
		{200, `{"code":200,"data":{"canPlay":false,"message":"请先登录","playUrl":null}}`},
		{401, `{"code":401,"message":"未登录"}`},
		{200, `{"code":401,"message":"登录已失效"}`},
	} {
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatal("anonymous request carried credentials")
			}
			return sourceFixtureResponse(request, response.status, response.body), nil
		})
		_, err := d.resolveSoraniMedia(context.Background(), Task{DramaID: "sorani:4736", Chapter: Chapter{ID: providerChapterID(sourceSorani, "4736", "66178")}})
		if !errors.Is(err, errSoraniLoginRequired) || strings.Contains(err.Error(), "暂无可用播放地址") {
			t.Fatal("login requirement was flattened", err)
		}
	}
}

func TestSoraniAccountConnectRefreshPersistAndLogout(t *testing.T) {
	refreshes, requests := 0, 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.sorani.cc" || request.Header.Get("Origin") != soraniSiteBaseURL {
			t.Fatal("credentials sent outside original API")
		}
		switch request.URL.Path {
		case "/sorani-cms/api/auth/profile":
			if request.Header.Get("Authorization") != "Bearer fixture-access-original" {
				t.Fatal("missing profile authorization")
			}
			return sourceFixtureResponse(request, 200, `{"code":200,"data":{"id":7,"nickname":"合成账号"}}`), nil
		case "/sorani-cms/api/auth/refresh":
			refreshes++
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer fixture-refresh-original" {
				t.Fatal("incorrect refresh request")
			}
			return sourceFixtureResponse(request, 200, `{"code":200,"data":{"token":"fixture-access-refreshed","refreshToken":"fixture-refresh-refreshed"}}`), nil
		case "/sorani-cms/api/video/episode/66178/play":
			requests++
			if request.Header.Get("Authorization") == "Bearer fixture-access-original" {
				return sourceFixtureResponse(request, 200, `{"code":200,"data":{"canPlay":false,"message":"请先登录","playUrl":null}}`), nil
			}
			if request.Header.Get("Authorization") != "Bearer fixture-access-refreshed" {
				t.Fatal("refreshed token missing")
			}
			return sourceFixtureResponse(request, 200, `{"code":200,"data":{"canPlay":true,"playUrl":"https://media.example/fixture.mp4"}}`), nil
		default:
			t.Fatal("unexpected request", request.URL.Path)
			return nil, errors.New("unexpected route")
		}
	})
	client := d.soraniClient()
	status, err := client.connectAccount(context.Background(), &soraniAccountSession{Token: "fixture-access-original", RefreshToken: "fixture-refresh-original"})
	if err != nil || status["authenticated"] != true || status["name"] != "合成账号" {
		t.Fatal("login state not connected", status, err)
	}
	media, err := d.resolveSoraniMedia(context.Background(), Task{DramaID: "sorani:4736", Chapter: Chapter{ID: providerChapterID(sourceSorani, "4736", "66178")}})
	if err != nil || media.URL != "https://media.example/fixture.mp4" || refreshes != 1 || requests != 2 {
		t.Fatal("authorized playback/one-time refresh failed", refreshes, requests, err)
	}
	if sourceSessionPersistent {
		sealed, err := os.ReadFile(client.accountPath())
		if err != nil || strings.Contains(string(sealed), "fixture-access") || strings.Contains(string(sealed), "fixture-refresh") {
			t.Fatal("plaintext credential persistence")
		}
		reopened := &Downloader{cfg: d.cfg, client: d.client}
		if session := reopened.soraniClient().accountSnapshot(); session == nil || session.Token != "fixture-access-refreshed" {
			t.Fatal("protected login did not survive reopening")
		}
	}
	status, err = client.disconnectAccount()
	if err != nil || status["authenticated"] != false || client.accountSnapshot() != nil {
		t.Fatal("logout did not clear credentials", err)
	}
	if sourceSessionPersistent {
		if _, err := os.Stat(client.accountPath()); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("protected session was not removed")
		}
	}
}

func TestSoraniAccountRejectsForeignOriginAndInsecureTLS(t *testing.T) {
	d := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe origin reached transport")
		return nil, errors.New("not allowed")
	})
	for _, sample := range []struct {
		base     string
		insecure bool
	}{{"https://foreign.example/api", false}, {soraniAPIBaseURL, true}} {
		d.cfg.InsecureTLS = sample.insecure
		client := &soraniAPIClient{downloader: d, base: sample.base, site: soraniSiteBaseURL}
		if _, err := client.connectAccount(context.Background(), &soraniAccountSession{Token: "fixture-access", RefreshToken: "fixture-refresh"}); err == nil {
			t.Fatal("unsafe authentication origin accepted")
		}
	}
}

func TestSoraniAccountRejectedRefreshClearsLogin(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 401, `{"code":401,"message":"请先登录"}`), nil
	})
	client := d.soraniClient()
	client.loadAccount()
	client.auth = &soraniAccountSession{Token: "fixture-expired-access", RefreshToken: "fixture-expired-refresh", epoch: client.authEpoch}
	_, err := client.get(context.Background(), "/video/episode/66178/play", nil, 65536)
	if !errors.Is(err, errSoraniLoginRequired) || !strings.Contains(err.Error(), "登录已过期") || client.accountStatus()["authenticated"] != false {
		t.Fatal("expired account not explicitly cleared", err)
	}
}

func TestSoraniAccountActionRespectsBuildScope(t *testing.T) {
	previous := buildAllSources
	defer func() { buildAllSources = previous }()
	buildAllSources = "false"
	if err := nativeAuthorizeInput(nativeInput{Action: "sourceAccount", Source: sourceSorani}); !errors.Is(err, errNativeBuildSource) {
		t.Fatal("restricted edition exposed source account")
	}
	buildAllSources = "true"
	if err := nativeAuthorizeInput(nativeInput{Action: "sourceAccount", Source: sourceSorani}); err != nil {
		t.Fatal(err)
	}
}
