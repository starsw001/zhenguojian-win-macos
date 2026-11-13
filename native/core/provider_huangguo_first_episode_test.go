package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// firstEpisodeFixture 提取首个分集Fixture。
func firstEpisodeFixture(firstLink string, last int) string {
	var body strings.Builder
	body.WriteString(`<h1>普通作品</h1>`)
	body.WriteString(firstLink)
	for index := 2; index <= last; index++ {
		fmt.Fprintf(&body, `<a href="/video/89/ep-%d/">%d</a>`, index, index)
	}
	return body.String()
}

// TestAIFirstEpisodeBaseLinkKeepsOriginalNumberAndPage 验证「AI First Episode Base Link Keeps Original Number And Page」回归场景。
func TestAIFirstEpisodeBaseLinkKeepsOriginalNumberAndPage(t *testing.T) {
	for _, label := range []string{"1", "01", "第1集", "第一集"} {
		for _, path := range []string{"/video/89", "/video/89/"} {
			body := firstEpisodeFixture(fmt.Sprintf(`<a href="%s">%s</a>`, path, label), 9)
			rows := parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89")
			if len(rows) != 9 || rows[0].URL != "https://catalog.example.org"+path {
				t.Fatal("explicit first episode base route omitted or changed", path, label, rows)
			}
			for index, row := range rows {
				if row.Index != index+1 || row.Key != fmt.Sprintf("ep-%d", index+1) {
					t.Fatal("original numbering or stable episode key lost", row)
				}
			}
		}
	}
}

// TestAIFirstEpisodeAliasesDeduplicateWithoutGuessing 验证「AI First Episode Aliases Deduplicate Without Guessing」回归场景。
func TestAIFirstEpisodeAliasesDeduplicateWithoutGuessing(t *testing.T) {
	body := firstEpisodeFixture(`<a href="/video/89/">1</a><a href="/video/89/ep-1/">第1集</a><a href="/video/89/?from=menu">1</a>`, 8)
	rows := parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89")
	if len(rows) != 8 || rows[0].Index != 1 {
		t.Fatal("first episode aliases counted repeatedly", rows)
	}
	body = `<a href="/video/89/">立即播放</a><a href="/video/900/">1</a><a href="https://other.example.org/video/89/">1</a>`
	if rows = parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89"); len(rows) != 0 {
		t.Fatal("unlabelled or foreign page invented a first episode", rows)
	}
}

// TestAIFirstEpisodeLabelConflictsFailClosed 验证「AI First Episode Label Conflicts Fail Closed」回归场景。
func TestAIFirstEpisodeLabelConflictsFailClosed(t *testing.T) {
	for _, body := range []string{
		`<a href="/video/89/ep-1/">2</a>`,
		`<a href="/video/89/ep-2/">第1集</a>`,
		`<a href="/video/89/" title="第2集">1</a>`,
	} {
		if rows := parseHuangguoAIEpisodes(body, "https://catalog.example.org/detail/89/", "89"); len(rows) != 0 {
			t.Fatal("conflicting episode label accepted", rows)
		}
	}
}

// TestAIFirstEpisodeDetailDoesNotAcceptMissingNumber 验证「AI First Episode Detail Does Not Accept Missing Number」回归场景。
func TestAIFirstEpisodeDetailDoesNotAcceptMissingNumber(t *testing.T) {
	old := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = old }()
	for _, sample := range []struct {
		body string
		want string
	}{
		{firstEpisodeFixture("", 9), "第1集"},
		{strings.ReplaceAll(firstEpisodeFixture(`<a href="/video/89/">1</a>`, 9), `<a href="/video/89/ep-4/">4</a>`, ""), "第4集"},
	} {
		engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(request, 200, sample.body), nil
		})
		_, err := engine.nativeDetail(context.Background(), nativeDrama{ID: "huangguoai:https://catalog.example.org/detail/89/", Source: sourceHuangguoAI, SourceID: "https://catalog.example.org/detail/89/", Title: "普通作品"})
		if err == nil || !strings.Contains(err.Error(), sample.want) {
			t.Fatal("missing episode silently accepted or renumbered", sample.want, err)
		}
	}
}

// TestAIFirstEpisodeCompleteDetailCountsAndOwnership 验证「AI First Episode Complete Detail Counts And Ownership」回归场景。
func TestAIFirstEpisodeCompleteDetailCountsAndOwnership(t *testing.T) {
	old := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = old }()
	for _, count := range []int{1, 9} {
		body := firstEpisodeFixture(`<a href="/video/89/">1</a>`, count) + `<a href="/video/777/ep-1/">1</a>`
		engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(request, 200, body), nil
		})
		response, err := engine.nativeDetail(context.Background(), nativeDrama{ID: "huangguoai:https://catalog.example.org/detail/89/", Source: sourceHuangguoAI, SourceID: "https://catalog.example.org/detail/89/", Title: "普通作品"})
		if err != nil {
			t.Fatal(err)
		}
		result := response.(map[string]any)
		chapters, drama := result["chapters"].([]Chapter), result["drama"].(nativeDrama)
		if len(chapters) != count || drama.Episodes != count || !drama.EpisodeCountVerified || chapterEpisodeNumber(chapters[0], 0) != 1 || chapters[0].PageURL != "https://catalog.example.org/video/89/" {
			t.Fatal("first episode, actual count or ownership lost", result)
		}
	}
}
