package core

import (
	"context"
	"net/http"
	"testing"
)

func TestSoraniLoginProbe(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `{"code":200,"data":{"episodeId":66178,"canPlay":false,"message":"请先登录","playUrl":null}}`), nil
	})
	_, err := d.resolveSoraniMedia(context.Background(), Task{DramaID: "sorani:4736", Chapter: Chapter{ID: providerChapterID(sourceSorani, "4736", "66178")}})
	if err == nil {
		t.Fatal("login-required response accepted as playable")
	}
	t.Log("OBSERVED:", err.Error())
}
