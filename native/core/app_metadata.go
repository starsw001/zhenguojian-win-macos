package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// nativeNeedsExtraMetadata 判断是否需要原生补充元数据。
func nativeNeedsExtraMetadata(drama nativeDrama) bool {
	return drama.Source == sourceHongguo && drama.OnlineDate == "" ||
		drama.Source == sourceHuangdou && (drama.Heat == "" || drama.VIP == nil)
}

// nativeExtraMetadata 返回原生补充元数据。
func (engine *nativeEngine) nativeExtraMetadata(ctx context.Context, drama nativeDrama) (nativeDrama, error) {
	source, id, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return drama, errors.New("剧集信息无效")
	}
	if !nativeNeedsExtraMetadata(drama) {
		return drama, nil
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, backgroundCatalogKey{}, true), 15*time.Second)
	defer cancel()
	var raw Drama
	switch source {
	case sourceHongguo:
		if !hongguoNumericID.MatchString(id) {
			return drama, errors.New("红果剧集 ID 无效")
		}
		body, err := engine.downloader.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(id), hongguoBaseURL+"/")
		if err != nil {
			return drama, err
		}
		raw, err = parseHongguoSortDetail(body, id)
		if err != nil {
			return drama, err
		}
	case sourceHuangdou:
		row, err := engine.downloader.huangdouDetail(ctx, id)
		if err != nil {
			return drama, err
		}
		raw = huangdouDramaFromMap(row)
		if raw.Heat == "" && drama.Heat == "" {
			var decoded any
			err = newHuangdouAPIClient(engine.downloader).call(ctx, "/drama/list", map[string]any{
				"keywords": firstNonEmpty(raw.Title, drama.Title), "page": "1", "page_size": "50",
			}, &decoded)
			if err != nil {
				return drama, err
			}
			for _, candidate := range huangdouList(decoded) {
				if strings.TrimPrefix(mapString(candidate, "id", "drama_id"), "rp_") == id {
					raw = mergeDramaMetadata(huangdouDramaFromMap(candidate), raw)
					break
				}
			}
		}
	}
	if raw.ID == drama.ID {
		count := drama.Episodes
		verified := drama.EpisodeCountVerified
		if ordinarySource(source) {
			if err := validateOrdinaryDetail(drama, raw, "", nil); err != nil {
				return drama, err
			}
		}
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
		if ordinarySource(source) {
			drama.Episodes = count
			drama.EpisodeCountVerified = verified
		}
	}
	return drama, ctx.Err()
}

// nativeMetadata 读取作品补充元数据并合并到当前记录。
func (engine *nativeEngine) nativeMetadata(ctx context.Context, drama nativeDrama) (any, error) {
	fresh, err := engine.nativeExtraMetadata(ctx, drama)
	if err != nil {
		return nil, err
	}
	warning := ""
	if err := engine.saveDetailMetadata(fresh); err != nil {
		warning = err.Error()
	}
	return map[string]any{"drama": fresh, "warning": warning}, nil
}
