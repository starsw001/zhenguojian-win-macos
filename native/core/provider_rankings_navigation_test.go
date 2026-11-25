package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func rankingNavigationFixture(t *testing.T) (map[string]string, []struct{ Group, Name, Path, UpstreamKey string }) {
	t.Helper()
	b, e := os.ReadFile("testdata/hongguo_ranking_navigation.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Pages    map[string]string
		Expected []struct{ Group, Name, Path, UpstreamKey string }
	}
	if json.Unmarshal(b, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	return fixture.Pages, fixture.Expected
}
func TestHongguoSourceNavigationGroupsAndTwentyRoutes(t *testing.T) {
	pages, expected := rankingNavigationFixture(t)
	groups, err := parseHongguoRankingGroups(pages["hot-drama"])
	if err != nil || len(groups) != 4 {
		t.Fatal(groups, err)
	}
	responsive := strings.Replace(pages["hot-drama"], ">全部</a>", "><span>全部</span><span aria-hidden=\"true\">全部</span></a>", 1)
	normalizedGroups, e := parseHongguoRankingGroups(responsive)
	if e != nil || normalizedGroups[0].Name != "全部" {
		t.Fatal("responsive duplicate category label", normalizedGroups, e)
	}
	boards := []rankingBoard{}
	for _, g := range groups {
		rows, e := parseHongguoRankingTypes(pages[g.Path], g)
		if e != nil {
			t.Fatal(e)
		}
		boards = append(boards, rows...)
	}
	if len(boards) != len(expected) {
		t.Fatal(len(boards), len(expected))
	}
	for i, b := range boards {
		want := expected[i]
		if b.CategoryName != want.Group || b.Name != want.Name || b.path != want.Path || b.upstreamKey != want.UpstreamKey {
			t.Fatal(i, b, want)
		}
		resolved, ok := findRankingBoard(b.ID)
		if !ok || resolved.path != b.path {
			t.Fatal(b)
		}
		if err := nativeAuthorizeInput(nativeInput{Action: "rankings", Board: b.ID}); err != nil {
			t.Fatal(b.ID, err)
		}
	}
	for _, raw := range []string{"https://evil.invalid/rank/hot-drama", "https://user:password@hongguoduanju.com/rank/hot-drama", "https://hongguoduanju.com:8443/rank/hot-drama", "/rank/hot-drama?page=2", "/rank/../hot-drama", "/rank/missing-drama", "//evil.invalid/rank/hot-drama"} {
		if _, ok := hongguoRankingLink(raw); ok {
			t.Fatal("unsafe ranking link", raw)
		}
	}
	prefixed := strings.Replace(pages["hot-drama"], ">热播榜</a>", ">全部/热播榜</a>", 1)
	normalized, err := parseHongguoRankingTypes(prefixed, groups[0])
	if err != nil || normalized[0].Name != "热播榜" {
		t.Fatal("screen-reader category prefix leaked into child labels", err)
	}
	bad := strings.Replace(pages["hot-comic-drama"], "/rank/new-comic-drama", "/rank/new-real-drama", 1)
	if _, err := parseHongguoRankingTypes(bad, groups[1]); err == nil {
		t.Fatal("cross-category board accepted")
	}
}
func TestHongguoRankingNavigationCacheAndOfflineFallback(t *testing.T) {
	pages, _ := rankingNavigationFixture(t)
	calls := 0
	fail := false
	e := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if fail {
			return nil, errors.New("fixture offline")
		}
		body, ok := pages[strings.TrimPrefix(r.URL.Path, "/rank/")]
		if !ok {
			t.Fatal(r.URL.String())
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	boards, err := e.downloader.loadHongguoRankingBoards(context.Background(), false)
	if err != nil || len(boards) != 20 || calls != 4 {
		t.Fatal(len(boards), calls, err)
	}
	if cached, err := e.downloader.loadHongguoRankingBoards(context.Background(), false); err != nil || len(cached) != 20 || calls != 4 {
		t.Fatal(len(cached), calls, err)
	}
	fail = true
	e.downloader.rankings.boardsFetchedAt = time.Now().Add(-6 * time.Minute)
	cached, err := e.downloader.loadHongguoRankingBoards(context.Background(), true)
	if err != nil || len(cached) != 20 || !strings.Contains(cached[0].Description, "刷新失败") {
		t.Fatal(cached, err)
	}
	e.downloader.rankings.boards = nil
	e.downloader.rankings.boardsLoaded = false
	cached, err = e.downloader.loadHongguoRankingBoards(context.Background(), false)
	if err != nil || len(cached) != 20 {
		t.Fatal("stored navigation was not reusable", err)
	}
	if _, err := parseHongguoRankingGroups("<h1>访问验证</h1>"); err == nil {
		t.Fatal("verification page accepted as navigation")
	}
}

func TestHongguoRankingPrimaryMetricAndUpcomingStatus(t *testing.T) {
	board, _ := hongguoRankingSpec("subscribe-drama")
	rows := rankingRows(1)
	rows[0]["primaryMetricText"] = "3555.6万期待"
	rows[0]["reservationOnly"] = true
	rows[0]["subscribeText"] = "268.2万人预约"
	rows[0]["favoriteText"] = "1335收藏"
	rows[0]["expectedPublishText"] = "预计12月18日上线"
	page, err := parseHongguoRanking(rankingFixture(board, 1, rows), board, 1)
	if err != nil {
		t.Fatal(err)
	}
	item := page.Items[0]
	if item.Metric != "3555.6万期待" || !item.ReservationOnly || !strings.Contains(item.Status, "268.2万人预约") || !strings.Contains(item.Status, "预计12月18日上线") {
		t.Fatal(item)
	}
	board, _ = hongguoRankingSpec("prestige-drama")
	rows = rankingRows(1)
	rows[0]["primaryMetricText"] = "9.9分"
	rows[0]["scoreText"] = "9.9分"
	page, err = parseHongguoRanking(rankingFixture(board, 1, rows), board, 1)
	if err != nil || page.Items[0].Metric != "9.9分" || page.Items[0].ReservationOnly {
		t.Fatal(page, err)
	}
}
