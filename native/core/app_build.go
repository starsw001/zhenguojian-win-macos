package core

import "errors"

var buildAllSources = "false"
var buildCatalogEpoch = ""

var errNativeBuildSource = errors.New("当前版本不包含此站源")

// nativeSourceAvailable 判断当前构建是否包含指定站源。
func nativeSourceAvailable(source string) bool {
	source = canonicalProviderSource(source)
	return source == sourceHongguo || buildAllSources == "true" && isHuangguoProviderSource(source)
}

// nativeDramaAvailable 判断指定作品是否属于当前构建支持的站源。
func nativeDramaAvailable(drama nativeDrama) bool {
	source, _, valid := splitProviderDramaID(drama.ID)
	return valid && nativeSourceAvailable(source) &&
		(drama.Source == "" || canonicalProviderSource(drama.Source) == source)
}

// nativeChapterAvailable 判断指定分集是否属于当前构建支持的站源。
func nativeChapterAvailable(drama nativeDrama, chapter Chapter) bool {
	if len(chapter.Alternatives) > nativePlaybackRouteLimit {
		return false
	}
	for _, alternate := range chapter.Alternatives {
		if len(alternate.Alternatives) != 0 || !sameEpisodeRoute(chapter, alternate) {
			return false
		}
	}
	if !nativeDramaAvailable(drama) {
		return false
	}
	source, _, _ := splitProviderDramaID(drama.ID)
	if chapter.Source != "" && canonicalProviderSource(chapter.Source) != source {
		return false
	}
	if chapterSource, _, valid := splitProviderDramaID(chapter.ID); valid && chapterSource != source {
		return false
	}
	return true
}

// nativeDownloadAvailable 判断指定下载条目是否属于当前构建支持的范围。
func nativeDownloadAvailable(job nativeDownloadJob) bool {
	return nativeChapterAvailable(job.Drama, job.Chapter)
}

// nativeAuthorizeInput 校验原生请求涉及的站源、作品和下载条目是否属于当前构建范围。
func nativeAuthorizeInput(input nativeInput) error {
	switch input.Action {
	case "recommendations", "cachedRecommendations", "suggestions", "danmaku":
		if !nativeSourceAvailable(sourceHongguo) {
			return errNativeBuildSource
		}
	case "rankings":
		board, found := findRankingBoard(input.Board)
		if !found || !nativeSourceAvailable(board.Source) {
			return errNativeBuildSource
		}
	case "catalog", "cached", "categories", "cachedCategories", "sourceStatus", "sourceJob", "cancelSourceJob", "saveSourceHealth", "sourceAccount":
		if !nativeSourceAvailable(input.Source) {
			return errNativeBuildSource
		}
		if input.Action == "sourceJob" && input.Drama.ID != "" &&
			(!nativeDramaAvailable(input.Drama) || sourceFromDramaID(input.Drama.ID) != canonicalProviderSource(input.Source)) {
			return errNativeBuildSource
		}
	case "cover", "prepareCover", "detail", "metadata", "resolve", "preload", "prepareHandoff", "enqueueDownloads", "localPlayback", "checkEpisodeConnection":
		if !nativeDramaAvailable(input.Drama) {
			return errNativeBuildSource
		}
		if (input.Action == "resolve" || input.Action == "preload" || input.Action == "prepareHandoff" || input.Action == "checkEpisodeConnection") && !nativeChapterAvailable(input.Drama, input.Chapter) {
			return errNativeBuildSource
		}
	}
	return nil
}
