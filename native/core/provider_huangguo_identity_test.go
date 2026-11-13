package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// identityEpisodeFixture 返回身份分集Fixture。
func identityEpisodeFixture(own, other int) string {
	var body strings.Builder
	body.WriteString(`<h1>普通作品</h1>`)
	for number := 1; number <= own; number++ {
		fmt.Fprintf(&body, `<a href="/video/89/ep-%d/">第%d集</a>`, number, number)
	}
	for number := 1; number <= other; number++ {
		fmt.Fprintf(&body, `<a href="/video/%d/ep-1/">其他作品推荐</a>`, 100+number)
	}
	return body.String()
}

// TestAIIdentityEightOwnEpisodesExcludeSeventeenRecommendations 验证「AI Identity Eight Own Episodes Exclude Seventeen Recommendations」回归场景。
func TestAIIdentityEightOwnEpisodesExcludeSeventeenRecommendations(t *testing.T) {
	rows := parseHuangguoAIEpisodes(identityEpisodeFixture(8, 17), "https://catalog.example.org/detail/89/", "89")
	if len(rows) != 8 {
		t.Fatal("foreign recommendations mixed into episode count", len(rows))
	}
	for index, row := range rows {
		if row.Index != index+1 || !strings.Contains(row.URL, "/video/89/") {
			t.Fatal("episode ownership or numbering", row)
		}
	}
}

// TestAIIdentityIgnoresForeignDomainAndRejectsConflictingNumber 验证「AI Identity Ignores Foreign Domain And Rejects Conflicting Number」回归场景。
func TestAIIdentityIgnoresForeignDomainAndRejectsConflictingNumber(t *testing.T) {
	body := identityEpisodeFixture(8, 0) + `<a href="https://other.example.org/video/89/ep-9/">第9集</a>`
	if len(parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89")) != 8 {
		t.Fatal("foreign domain counted as current site")
	}
	body = `<a href="/video/89/ep-1/">第2集</a>`
	if len(parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89")) != 0 {
		t.Fatal("conflicting number was silently renumbered")
	}
}

// TestAIIdentityCatalogAndDetailStayOnSelectedDomain 验证「AI Identity Catalog And Detail Stay On Selected Domain」回归场景。
func TestAIIdentityCatalogAndDetailStayOnSelectedDomain(t *testing.T) {
	oldBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = oldBuild }()
	calls := map[string]int{}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		calls[request.URL.Host]++
		if request.URL.Host == "catalog.example.org" {
			if request.Header.Get("Cache-Control") != "no-cache" {
				t.Fatal("detail reused intermediary cache")
			}
			return sourceFixtureResponse(request, 200, identityEpisodeFixture(8, 17)), nil
		}
		return sourceFixtureResponse(request, 200, identityEpisodeFixture(25, 0)), nil
	})
	engine.downloader.cfg.HuangguoAIURL = "https://fallback.example.org"
	card := `<div class="hg-drama-card" data-track-id="89" data-track-title="普通作品"><a href="/detail/89/"><span class="hg-drama-card__episode" data-ep-base="8">8集</span></a></div>`
	a := parseHuangguoAIDramaCards(card, "https://catalog.example.org/", "")
	b := parseHuangguoAIDramaCards(card, "https://fallback.example.org/", "")
	if len(a) != 1 || len(b) != 1 || a[0].ID == b[0].ID {
		t.Fatal("same numeric identifiers collided across domains")
	}
	response, err := engine.nativeDetail(context.Background(), nativeNormalize(a[0]))
	if err != nil {
		t.Fatal(err)
	}
	result := response.(map[string]any)
	if len(result["chapters"].([]Chapter)) != 8 || result["drama"].(nativeDrama).Episodes != 8 || !result["drama"].(nativeDrama).EpisodeCountVerified || calls["fallback.example.org"] != 0 {
		t.Fatal("detail moved to fallback domain or stale count", calls)
	}
}

// TestAIIdentityWrongTitleAndLegacyEntryFailClosed 验证「AI Identity Wrong Title And Legacy Entry Fail Closed」回归场景。
func TestAIIdentityWrongTitleAndLegacyEntryFailClosed(t *testing.T) {
	oldBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = oldBuild }()
	requests := 0
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		requests++
		return sourceFixtureResponse(request, 200, strings.ReplaceAll(identityEpisodeFixture(8, 0), "普通作品", "另一作品")), nil
	})
	selected := nativeDrama{ID: "huangguoai:https://catalog.example.org/detail/89/", Source: sourceHuangguoAI, SourceID: "https://catalog.example.org/detail/89/", Title: "普通作品"}
	if _, err := engine.nativeDetail(context.Background(), selected); err == nil {
		t.Fatal("different work title accepted")
	}
	requests = 0
	if _, err := engine.nativeDetail(context.Background(), nativeDrama{ID: "huangguoai:89", Source: sourceHuangguoAI, SourceID: "89", Title: "普通作品"}); err == nil || requests != 0 {
		t.Fatal("legacy unscoped entry silently moved to current domain")
	}
}

// TestAIIdentityRejectsForeignPlaybackBeforeAnyRequest 验证「AI Identity Rejects Foreign Playback Before Any Request」回归场景。
func TestAIIdentityRejectsForeignPlaybackBeforeAnyRequest(t *testing.T) {
	requests := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		requests++
		return sourceFixtureResponse(request, 200, "unexpected"), nil
	})
	id := "https://catalog.example.org/detail/89/"
	chapter := Chapter{ID: providerChapterID(sourceHuangguoAI, id, "ep-1"), Source: sourceHuangguoAI, PageURL: "https://other.example.org/video/89/ep-1/"}
	if _, err := d.resolveProviderMedia(context.Background(), Task{DramaID: providerDramaID(sourceHuangguoAI, id), Chapter: chapter}); err == nil || requests != 0 {
		t.Fatal("foreign page reached network or media resolution")
	}
}

// TestAIIdentityFreshDetailSurvivesStagedCardCommit 验证「AI Identity Fresh Detail Survives Staged Card Commit」回归场景。
func TestAIIdentityFreshDetailSurvivesStagedCardCommit(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	id := "huangguoai:https://catalog.example.org/detail/89/"
	engine.huangguoFreshMetadata = map[string]nativeDrama{id: {ID: id, Source: sourceHuangguoAI, Title: "普通作品", Episodes: 8, EpisodeCountVerified: true}}
	snapshot := huangguoCatalogSnapshot{origin: "https://catalog.example.org", rows: []nativeDrama{{ID: id, Source: sourceHuangguoAI, Title: "普通作品", Episodes: 25}}, pages: 1}
	if err := engine.commitHuangguoCatalog(snapshot); err != nil {
		t.Fatal(err)
	}
	row := engine.catalogs[sourceHuangguoAI][0]
	if row.Episodes != 8 || !row.EpisodeCountVerified {
		t.Fatal("staged card count overwrote freshly checked detail", row)
	}
}
