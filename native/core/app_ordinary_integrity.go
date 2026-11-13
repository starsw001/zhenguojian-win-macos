package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type nativeOrdinaryIntegrity struct {
	Declared        int   `json:"declared"`
	Actual          int   `json:"actual"`
	RawRecords      int   `json:"rawRecords"`
	DuplicateRows   int   `json:"duplicateRows"`
	InferredNumbers int   `json:"inferredNumbers"`
	MissingNumbers  []int `json:"missingNumbers"`
}

var ordinaryEpisodeLabel = regexp.MustCompile(`^(?:第\s*)?0*([0-9]+)\s*(?:集|话|話|回|期)$`)

// ordinaryChapterNumber 取得普通站源分集的明确集号。
func ordinaryChapterNumber(chapter Chapter, fallback int) (int, bool, error) {
	raw := strings.TrimSpace(string(chapter.CurrentEpisode))
	if raw == "" || raw == "null" {
		if label := ordinaryEpisodeLabel.FindStringSubmatch(strings.TrimSpace(chapter.Title)); len(label) == 2 {
			number, _ := strconv.Atoi(label[1])
			if number > 0 && number <= 100000 {
				return number, false, nil
			}
		}
		return fallback, true, nil
	}
	var value any
	if json.Unmarshal(chapter.CurrentEpisode, &value) != nil {
		return 0, false, errors.New("源站分集编号无效，未猜测集数")
	}
	var number int
	switch value := value.(type) {
	case string:
		number, _ = strconv.Atoi(strings.TrimSpace(value))
	case float64:
		if value == math.Trunc(value) && value > 0 && value <= 100000 {
			number = int(value)
		}
	}
	if number < 1 || number > 100000 {
		return 0, false, errors.New("源站分集编号不在支持范围内，未改成其他集数")
	}
	return number, false, nil
}

// ordinaryChapterMedia 提取普通站源分集的媒体标识。
func ordinaryChapterMedia(chapter Chapter) string {
	if address := strings.TrimSpace(chapter.VideoURL); address != "" {
		return address
	}
	if _, _, identified := maccmsEpisodeRoute(chapter.PageURL); identified {
		return chapter.PageURL
	}
	return ""
}

// prepareOrdinaryChapters 规整普通站源的分集归属、编号与顺序。
func prepareOrdinaryChapters(raw Drama, chapters []Chapter) ([]Chapter, nativeOrdinaryIntegrity, error) {
	info := nativeOrdinaryIntegrity{Declared: nativeNormalize(raw).Episodes, RawRecords: len(chapters), MissingNumbers: []int{}}
	result := make([]Chapter, 0, len(chapters))
	numbers, ids, media := map[int]int{}, map[string]int{}, map[string]int{}
	maximum := max(0, min(info.Declared, 100000))
	for index, chapter := range chapters {
		number, inferred, err := ordinaryChapterNumber(chapter, index+1)
		if err != nil {
			return nil, info, err
		}
		if label := ordinaryEpisodeLabel.FindStringSubmatch(strings.TrimSpace(chapter.Title)); len(label) == 2 {
			expected, _ := strconv.Atoi(label[1])
			if !inferred && number != expected {
				return nil, info, fmt.Errorf("源站第%d条分集标题与编号不符（%s / %d），未混入列表", index+1, chapter.Title, number)
			}
		}
		address := ordinaryChapterMedia(chapter)
		if other, found := ids[chapter.ID]; chapter.ID != "" && found && other != number {
			return nil, info, errors.New("源站将同一分集标识用于不同集数，已停止加载")
		}
		if other, found := media[address]; address != "" && found && other != number {
			return nil, info, errors.New("源站将同一播放入口用于不同集数，已停止加载")
		}
		if at, duplicate := numbers[number]; duplicate {
			previous := result[at]
			if previous.ID != chapter.ID || ordinaryChapterMedia(previous) != address || previous.PageURL != chapter.PageURL {
				return nil, info, fmt.Errorf("源站第%d集有互相冲突的分集记录，未任意选择或拼接", number)
			}
			info.DuplicateRows++
			if previous.VIP && !chapter.VIP {
				chapter.CurrentEpisode = rawEpisode(number)
				result[at] = chapter
			}
			continue
		}
		if inferred {
			info.InferredNumbers++
		}
		chapter.CurrentEpisode = rawEpisode(number)
		numbers[number] = len(result)
		if chapter.ID != "" {
			ids[chapter.ID] = number
		}
		if address != "" {
			media[address] = number
		}
		maximum = max(maximum, number)
		result = append(result, chapter)
	}
	info.Actual = len(result)
	first := 1
	if raw.Source == sourceSorani && info.Actual > 0 && info.Declared == info.Actual && info.RawRecords == info.Actual {
		first = maximum
		for number := range numbers {
			first = min(first, number)
		}
	}
	for number := first; number <= maximum; number++ {
		if _, found := numbers[number]; !found {
			info.MissingNumbers = append(info.MissingNumbers, number)
		}
	}
	sort.SliceStable(result, func(a, b int) bool {
		return ordinaryChapterOrder(result[a], a+1) < ordinaryChapterOrder(result[b], b+1)
	})
	return result, info, nil
}

// ordinaryChapterOrder 确定普通站源分集的排序顺序。
func ordinaryChapterOrder(chapter Chapter, fallback int) int {
	number, _, _ := ordinaryChapterNumber(chapter, fallback)
	return number
}
