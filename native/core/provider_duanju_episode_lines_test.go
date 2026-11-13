package core

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/net/html"
	"net/http"
	"strings"
	"testing"
)

// TestOrdinaryMaccmsSelectsOneOwnedIndividualLine 验证「Ordinary Maccms Selects One Owned Individual Line」回归场景。
func TestOrdinaryMaccmsSelectsOneOwnedIndividualLine(t *testing.T) {
	var input []providerEpisode
	input = append(input, providerEpisode{Title: "全集", URL: "/index.php/vod/play/id/2594/sid/2/nid/1.html"})
	for n := 1; n <= 32; n++ {
		input = append(input, providerEpisode{Title: fmt.Sprintf("第%02d集", n), URL: fmt.Sprintf("/index.php/vod/play/id/2594/sid/1/nid/%d.html", n)})
	}
	for n := 1; n <= 100; n++ {
		input = append(input, providerEpisode{Title: fmt.Sprintf("第%d集", n), URL: fmt.Sprintf("/vodplay/999-1-%d.html", n)})
	}
	got := maccmsSelectEpisodeLine(input, "2594")
	if len(got) != 32 || got[0].Title != "第01集" {
		t.Fatal("compilation or foreign work leaked", got)
	}
	for _, ep := range got {
		if !strings.Contains(ep.URL, "/id/2594/sid/1/") {
			t.Fatal(ep)
		}
	}
}

// TestOrdinaryMaccmsOnlyCompilationRemainsAvailable 验证「Ordinary Maccms Only Compilation Remains Available」回归场景。
func TestOrdinaryMaccmsOnlyCompilationRemainsAvailable(t *testing.T) {
	input := []providerEpisode{{Title: "全集", URL: "/play/123-2-1.html"}}
	got := maccmsSelectEpisodeLine(input, "123")
	if len(got) != 1 || got[0].Index != 1 || got[0].Title != "全集" {
		t.Fatal(got)
	}
	if len(maccmsSelectEpisodeLine(input, "999")) != 0 {
		t.Fatal("foreign-only playlist accepted")
	}
}

// TestOrdinaryMaccmsUnknownRoutesStayCompatibleAndDeduplicated 验证「Ordinary Maccms Unknown Routes Stay Compatible And Deduplicated」回归场景。
func TestOrdinaryMaccmsUnknownRoutesStayCompatibleAndDeduplicated(t *testing.T) {
	input := []providerEpisode{{Title: "全集", URL: "/opaque/a"}, {Title: "第1集", URL: "/opaque/b"}, {Title: "第1集", URL: "/opaque/c"}, {Title: "第2集", URL: "/opaque/d"}}
	got := maccmsSelectEpisodeLine(input, "123")
	if len(got) != 2 || got[0].URL != "/opaque/b" || got[1].Index != 2 {
		t.Fatal(got)
	}
}

// TestOrdinaryMaccmsUnknownCatalogCountIsNotInvented 验证「Ordinary Maccms Unknown Catalog Count Is Not Invented」回归场景。
func TestOrdinaryMaccmsUnknownCatalogCountIsNotInvented(t *testing.T) {
	for value, want := range map[string]int{"全集": 0, "已完结": 0, "": 0, "更新至12集": 12, "全32集": 32, "32": 32} {
		if got := maccmsCatalogEpisodeCount(value); got != want {
			t.Fatal(value, got, want)
		}
	}
}

// TestOrdinaryMaccmsDetailPosterDoesNotUseHeaderLogo 验证「Ordinary Maccms Detail Poster Does Not Use Header Logo」回归场景。
func TestOrdinaryMaccmsDetailPosterDoesNotUseHeaderLogo(t *testing.T) {
	document, err := html.Parse(strings.NewReader(`<html><img class="PcHeader_logoTxt" src="/upload/site/logo.png"><img class="DramaDetail_bookCover" alt="情深似海终成空" src="https://poster.example.test/actual.jpg"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if cover := maccmsDetailCover(document, "https://site.example.test/detail/2594"); cover != "https://poster.example.test/actual.jpg" {
		t.Fatal("header logo used as real poster", cover)
	}
	document, _ = html.Parse(strings.NewReader(`<html><img class="PcHeader_logoTxt" src="/upload/site/logo.png"><img src="/assets/search.png"></html>`))
	if cover := maccmsDetailCover(document, "https://site.example.test/detail/2594"); cover != "" {
		t.Fatal("logo-only page fabricated a poster", cover)
	}
}

// TestOrdinaryFanguoLegacyKeyPreservesRecordIdentity 验证「Ordinary Fanguo Legacy Key Preserves Record Identity」回归场景。
func TestOrdinaryFanguoLegacyKeyPreservesRecordIdentity(t *testing.T) {
	for _, key := range []string{"2015#jiuzhou", "2015~aml1emhvdQ"} {
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Query().Get("duanjuId") != "2015" || r.URL.Query().Get("source") != "jiuzhou" {
				t.Fatal(r.URL)
			}
			return sourceFixtureResponse(r, 200, `{"result":{"duanjuId":"2015","source":"jiuzhou","title":"普通作品","episodeList":[{"index":1,"playUrl":"https://media.example.test/1.mp4"}]}}`), nil
		})
		drama, chapters, err := d.fetchFanguoDetail(context.Background(), key)
		if err != nil || len(chapters) != 1 || drama.ID != "fanguo:"+key || chapters[0].ID != "fanguo:"+key+":1" {
			t.Fatal(drama, chapters, err)
		}
	}
}

// TestOrdinaryAPIWrongOwnerIsRejectedBeforeChapterMapping 验证「Ordinary API Wrong Owner Is Rejected Before Chapter Mapping」回归场景。
func TestOrdinaryAPIWrongOwnerIsRejectedBeforeChapterMapping(t *testing.T) {
	for _, source := range []string{sourceMaoguo, sourceFanguo} {
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			if source == sourceMaoguo {
				return sourceFixtureResponse(r, 200, `{"data":{"playlet_id":"999","title":"同名作品","play_list":[{"sort":1,"video_url":"https://media.example.test/1.mp4"}]}}`), nil
			}
			return sourceFixtureResponse(r, 200, `{"result":{"duanjuId":"999","title":"同名作品","episodeList":[{"index":1,"playUrl":"https://media.example.test/1.mp4"}]}}`), nil
		})
		if _, _, err := d.fetchDuanjuDetail(context.Background(), source, "123"); err == nil {
			t.Fatal("wrong API owner accepted", source)
		}
	}
}

// TestOrdinaryDetailWarnsAboutDeclaredButMissingEpisodes 验证「Ordinary Detail Warns About Declared But Missing Episodes」回归场景。
func TestOrdinaryDetailWarnsAboutDeclaredButMissingEpisodes(t *testing.T) {
	raw := Drama{ID: "maoguo:22420", Title: "普通作品", EpisodeCount: json.Number("61")}
	var chapters []Chapter
	for n := 1; n <= 61; n++ {
		if n%10 != 3 {
			chapters = append(chapters, Chapter{CurrentEpisode: rawEpisode(n)})
		}
	}
	warning := ordinaryDetailCountWarning(raw, chapters)
	if len(chapters) != 55 || !strings.Contains(warning, "标注61集") || !strings.Contains(warning, "返回55") || !strings.Contains(warning, "第3、13") {
		t.Fatal(warning, len(chapters))
	}
	if len(chapters) != 55 {
		t.Fatal("missing episodes fabricated")
	}
	raw.EpisodeCount = json.Number("55")
	if warning := ordinaryDetailCountWarning(raw, chapters); !strings.Contains(warning, "第3、13") {
		t.Fatal("equal count hid gaps in original episode numbers", warning)
	}
}
