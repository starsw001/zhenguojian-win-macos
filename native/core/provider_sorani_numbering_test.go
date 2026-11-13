package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSoraniNumberingAcceptsIntegralJSONNumbers 验证「Sorani Numbering Accepts Integral JSON Numbers」回归场景。
func TestSoraniNumberingAcceptsIntegralJSONNumbers(t *testing.T) {
	for _, value := range []any{json.Number("14.0"), json.Number("14"), float64(14), "14.000", "1.4e1"} {
		number, err := soraniEpisodeNumber(value, "第14集", 1)
		if err != nil || number != 14 {
			t.Fatal("valid integral source number replaced by row position", value, number, err)
		}
	}
	if number, err := soraniEpisodeNumber(nil, "第13集", 1); err != nil || number != 13 {
		t.Fatal("missing order did not preserve explicit episode label", number, err)
	}
}

// TestSoraniNumberingRejectsFractionsAndInvalidExplicitValues 验证「Sorani Numbering Rejects Fractions And Invalid Explicit Values」回归场景。
func TestSoraniNumberingRejectsFractionsAndInvalidExplicitValues(t *testing.T) {
	for _, value := range []any{json.Number("14.5"), "NaN", "Inf", "-1", "0", "100001", "", "not-a-number"} {
		if _, err := soraniEpisodeNumber(value, "第14集", 1); err == nil {
			t.Fatal("invalid source number was silently replaced", value)
		}
	}
}

// TestSoraniNumberingPartKeepsSourceWindowAndDetectsInternalGap 验证「Sorani Numbering Part Keeps Source Window And Detects Internal Gap」回归场景。
func TestSoraniNumberingPartKeepsSourceWindowAndDetectsInternalGap(t *testing.T) {
	for _, sample := range []struct {
		numbers []int
		missing int
	}{
		{[]int{14, 15, 16}, 0},
		{[]int{14, 16, 17}, 15},
	} {
		chapters := []Chapter{}
		for _, number := range sample.numbers {
			chapters = append(chapters, Chapter{ID: "part:" + string(rawEpisode(number)), Source: sourceSorani, Title: "第" + string(rawEpisode(number)) + "集", CurrentEpisode: rawEpisode(number)})
		}
		_, info, err := prepareOrdinaryChapters(Drama{Source: sourceSorani, TotalEpisode: 3}, chapters)
		if err != nil || info.Actual != 3 || sample.missing == 0 && len(info.MissingNumbers) != 0 || sample.missing > 0 && (len(info.MissingNumbers) != 1 || info.MissingNumbers[0] != sample.missing) {
			t.Fatal("source part window or internal missing number wrong", info, err)
		}
	}
	_, other, err := prepareOrdinaryChapters(Drama{Source: sourceMaoguo, TotalEpisode: 3}, []Chapter{{ID: "2", CurrentEpisode: rawEpisode(2)}, {ID: "3", CurrentEpisode: rawEpisode(3)}})
	if err != nil || len(other.MissingNumbers) != 1 || other.MissingNumbers[0] != 1 {
		t.Fatal("other sources lost first-episode gap protection", other, err)
	}
}

// TestSoraniNumberingRejectsForeignWorkEpisode 验证「Sorani Numbering Rejects Foreign Work Episode」回归场景。
func TestSoraniNumberingRejectsForeignWorkEpisode(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `{"code":200,"data":{"id":4783,"title":"普通作品","episodeCount":1,"episodes":[{"id":1,"videoId":9999,"episodeOrder":1.0,"episodeLabel":"第1集"}]}}`), nil
	})
	if _, _, err := d.fetchSoraniDetail(context.Background(), "4783"); err == nil || !strings.Contains(err.Error(), "不属于当前作品") {
		t.Fatal("foreign work episode accepted", err)
	}
}
