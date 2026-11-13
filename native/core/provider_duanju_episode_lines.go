package core

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var maccmsEpisodeRoutes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)/(?:index\.php/)?vod/play/id/(\d+)/sid/(\d+)/nid/(\d+)(?:[./]|$)`),
	regexp.MustCompile(`(?i)/(?:play|vodplay|zywplay|xzyxpy)/(\d+)-(\d+)-(\d+)(?:[./]|$)`),
}
var maccmsCompilationTitle = regexp.MustCompile(`全集|全剧|完整版|合集|合辑`)

// maccmsEpisodeRoute 返回MACCMS分集线路。
func maccmsEpisodeRoute(address string) (string, string, bool) {
	parsed, err := url.Parse(address)
	if err != nil {
		return "", "", false
	}
	for _, pattern := range maccmsEpisodeRoutes {
		if match := pattern.FindStringSubmatch(parsed.Path); len(match) == 4 {
			return match[1], match[2], true
		}
	}
	return "", "", false
}

// maccmsSelectEpisodeLine 选择MACCMS分集Line。
func maccmsSelectEpisodeLine(episodes []providerEpisode, sourceID string) []providerEpisode {
	groups := map[string][]providerEpisode{}
	var order []string
	for _, episode := range episodes {
		owner, line, identified := maccmsEpisodeRoute(episode.URL)
		if identified && owner != sourceID {
			continue
		}
		if !identified {
			line = "unscoped"
		}
		if _, found := groups[line]; !found {
			order = append(order, line)
		}
		groups[line] = append(groups[line], episode)
	}
	var best []providerEpisode
	bestNumbered := -1
	for _, line := range order {
		values := groups[line]
		numbered := 0
		for _, episode := range values {
			if episodeIndex(episode.Title, 0) > 0 {
				numbered++
			}
		}
		seen := map[int]bool{}
		var selected []providerEpisode
		for _, episode := range values {
			number := episodeIndex(episode.Title, 0)
			if number <= 0 && maccmsCompilationTitle.MatchString(episode.Title) && numbered > 0 {
				continue
			}
			if number <= 0 {
				number = len(selected) + 1
			}
			if seen[number] {
				continue
			}
			seen[number] = true
			episode.Index = number
			episode.Key = strconv.Itoa(number)
			selected = append(selected, episode)
		}
		score := 0
		if numbered > 0 {
			score = len(selected)
		}
		if score > bestNumbered || score == bestNumbered && len(selected) > len(best) {
			best, bestNumbered = selected, score
		}
	}
	return best
}

// maccmsCatalogEpisodeCount 返回MACCMS目录集数。
func maccmsCatalogEpisodeCount(remark string) int {
	if number, err := strconv.Atoi(strings.TrimSpace(remark)); err == nil && number > 0 {
		return number
	}
	return episodeIndex(remark, 0)
}
