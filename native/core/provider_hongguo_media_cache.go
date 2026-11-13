package core

import (
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"
)

type hongguoFreshMediaKey struct{}

type hongguoMediaCacheEntry struct {
	media   providerMedia
	expires time.Time
}

type hongguoMediaCache struct {
	mu         sync.Mutex
	entries    map[string]hongguoMediaCacheEntry
	generation uint64
}

// clear 清除当前保存的媒体解析缓存。
func (c *hongguoMediaCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.entries)
}

// get 按键读取尚在有效期内的媒体解析缓存。
func (c *hongguoMediaCache) get(key string, fresh bool, now time.Time) (providerMedia, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && !fresh && now.Before(entry.expires) {
		return cloneHongguoMedia(entry.media), c.generation, true
	}
	delete(c.entries, key)
	return providerMedia{}, c.generation, false
}

// put 保存媒体解析缓存并维护有效期与容量。
func (c *hongguoMediaCache) put(key string, generation uint64, media providerMedia, now time.Time) {
	expires := now.Add(15 * time.Second)
	for _, m := range append([]providerMedia{media}, media.Variants...) {
		address, err := url.Parse(m.URL)
		if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || m.credentials != nil || m.Playlist != "" {
			return
		}
		query := address.Query()
		for _, field := range []string{"expire", "expires", "expire_at", "x-expires", "deadline"} {
			if raw := query.Get(field); raw != "" {
				seconds, err := strconv.ParseInt(raw, 10, 64)
				if err != nil || seconds < 1_000_000_000 || seconds > 9_999_999_999 {
					return
				}
				if at := time.Unix(seconds, 0).Add(-2 * time.Second); at.Before(expires) {
					expires = at
				}
			}
		}
	}
	if !expires.After(now) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]hongguoMediaCacheEntry)
	}
	oldest := ""
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
			continue
		}
		if oldest == "" || entry.expires.Before(c.entries[oldest].expires) {
			oldest = key
		}
	}
	if len(c.entries) >= 32 {
		delete(c.entries, oldest)
	}
	c.entries[key] = hongguoMediaCacheEntry{cloneHongguoMedia(media), expires}
}

// cloneHongguoMedia 复制红果媒体。
func cloneHongguoMedia(media providerMedia) providerMedia {
	media.CENCKey = slices.Clone(media.CENCKey)
	media.HLSKey = slices.Clone(media.HLSKey)
	media.Variants = slices.Clone(media.Variants)
	for i := range media.Variants {
		media.Variants[i] = cloneHongguoMedia(media.Variants[i])
	}
	return media
}
