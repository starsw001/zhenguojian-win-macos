package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const hongguoProtectedFallbackPath = "/api/video/protected/fallback-play-info"

func (d *Downloader) resolveHongguoProtectedWebMedia(ctx context.Context, pageURL string, preview map[string]any, videoID string) (providerMedia, error) {
	payload, err := json.Marshal(struct {
		ItemID string `json:"item_id"`
	}{ItemID: videoID})
	if err != nil {
		return providerMedia{}, err
	}
	ctx = context.WithValue(ctx, nativeNoRedirectKey{}, true)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoBaseURL+hongguoProtectedFallbackPath, bytes.NewReader(payload))
	if err != nil {
		return providerMedia{}, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", hongguoBaseURL)
	request.Header.Set("Referer", pageURL)
	request.Header.Set("User-Agent", userAgent)
	response, err := d.doCatalogRequestWithTimeout(request, 10*time.Second)
	if err != nil {
		return providerMedia{}, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if readErr != nil {
		return providerMedia{}, readErr
	}
	if len(body) > 1<<20 {
		return providerMedia{}, errors.New("红果官网完整片源响应过大")
	}
	if response.StatusCode != http.StatusOK {
		return providerMedia{}, d.catalogResponseError(request, response, body)
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return providerMedia{}, errors.New("红果官网完整片源响应格式无效")
	}
	if code := mapString(nestedMap(result, "base_resp"), "StatusCode", "status_code"); code != "0" {
		return providerMedia{}, fmt.Errorf("红果官网完整片源返回状态 %s", firstNonEmpty(code, "unknown"))
	}
	if id := mapString(result, "item_id", "video_id"); id != "" && id != videoID {
		return providerMedia{}, errors.New("红果官网返回了其他分集")
	}
	addresses := hongguoMediaAddresses(result)
	if len(addresses) == 0 {
		return providerMedia{}, errors.New("红果官网没有返回完整地址，或返回了不受支持的服务地址")
	}
	expiresUnix, err := strconv.ParseInt(mapString(result, "url_expire"), 10, 64)
	if err != nil || expiresUnix <= time.Now().Unix()+5 {
		return providerMedia{}, errors.New("红果官网完整片源凭证已过期")
	}
	durationSeconds, err := strconv.ParseFloat(mapString(preview, "durationSeconds"), 64)
	if err != nil || durationSeconds <= 0 {
		return providerMedia{}, errors.New("红果官网未返回本集完整时长")
	}
	width, _ := strconv.Atoi(mapString(preview, "width"))
	height, _ := strconv.Atoi(mapString(preview, "height"))
	quality := width
	if quality <= 0 || height > 0 && height < quality {
		quality = height
	}
	var routes []providerMedia
	for _, address := range addresses {
		parsed, err := url.Parse(address)
		if err != nil || strings.Contains(strings.ToLower(parsed.Path), "/video/public/") {
			continue
		}
		routes = append(routes, providerMedia{
			Direct: true, URL: address, Referer: pageURL,
			Duration: time.Duration(durationSeconds * float64(time.Second)), Quality: quality,
			credentials: &providerMediaCredentials{origin: providerMediaOrigin(parsed), referer: pageURL, userAgent: userAgent, expires: time.Unix(expiresUnix, 0)},
		})
	}
	if len(routes) == 0 {
		return providerMedia{}, errors.New("红果官网只返回公开预览，未用作正片")
	}
	return d.expandHongguoCDNRoutes(ctx, mergeHongguoMediaRoutes(routes)), nil
}
