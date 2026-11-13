package core

import (
	"context"
	"duanjuapp/native/secureupdate"
	"errors"
	"time"
)

// passiveRelease 取得后台版本检查结果，供界面显示更新提示。
func (engine *nativeEngine) passiveRelease(ctx context.Context, q updateQuery) (updateResult, error) {
	out := updateResult{Current: q.Version, PageURL: secureupdate.ReleasesURL}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	latest, e := secureupdate.LatestPublishedRelease(c, engine.updateClient())
	if errors.Is(e, secureupdate.ErrNoPublishedRelease) {
		out.NoRelease = true
		return out, nil
	}
	if e != nil {
		out.Error = e.Error()
		return out, nil
	}
	out.Version = latest.Version()
	out.Available = latest.NewerThan(q.Version)
	out.PageURL = secureupdate.ReleasesURL + "/tag/" + latest.Tag
	return out, nil
}
