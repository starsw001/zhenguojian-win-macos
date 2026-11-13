package core

import (
	"fmt"
	"strconv"
	"strings"
)

// huangdouVIPFlag 解析黄豆响应中的付费标志。
func huangdouVIPFlag(item map[string]any) *bool {
	var known *bool
	for _, key := range []string{"is_vip", "isVip"} {
		value := strings.ToLower(mapString(item, key))
		if value == "true" || value == "1" || value == "y" {
			result := true
			return &result
		}
		if value == "false" || value == "0" || value == "n" {
			result := false
			known = &result
		}
	}
	switch strings.ToLower(mapString(item, "pay_type")) {
	case "vip", "coin", "money":
		result := true
		return &result
	case "free":
		result := false
		known = &result
	}
	for _, episode := range huangdouVIPEpisodes(item) {
		if episode > 0 {
			result := true
			return &result
		}
	}
	for _, episode := range huangdouList(item["episodes"]) {
		switch strings.ToLower(mapString(episode, "type")) {
		case "vip", "coin", "money":
			result := true
			return &result
		}
	}
	return known
}

// huangdouVIPEpisodes 从黄豆详情中收集付费分集标记。
func huangdouVIPEpisodes(item map[string]any) []int {
	var result []int
	if values, ok := item["vip_episodes"].([]any); ok {
		for _, value := range values {
			if index, err := strconv.Atoi(fmt.Sprint(value)); err == nil && index > 0 && index <= 10000 {
				result = append(result, index)
			}
		}
	}
	return result
}

// huangdouEpisodeVIP 判断指定黄豆分集是否标记为付费内容。
func huangdouEpisodeVIP(detail, episode map[string]any, seq int) bool {
	switch strings.ToLower(mapString(episode, "type")) {
	case "vip", "coin", "money":
		return true
	case "free":
		return false
	}
	for _, index := range huangdouVIPEpisodes(detail) {
		if index == seq {
			return true
		}
	}
	if flag := huangdouVIPFlag(episode); flag != nil {
		return *flag
	}
	switch strings.ToLower(mapString(detail, "pay_type")) {
	case "vip", "coin", "money":
		free, err := strconv.Atoi(mapString(detail, "free_episodes"))
		return err == nil && free >= 0 && seq > free
	}
	return false
}
