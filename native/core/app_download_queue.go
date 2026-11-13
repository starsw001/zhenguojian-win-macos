package core

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type nativeDownloadSourceKey struct{}
type nativeDownloadManagerKey struct{}

// nativeDownloadQueueDelay 下载原生队列延迟。
func nativeDownloadQueueDelay() time.Duration {
	return time.Duration(100+rand.IntN(401)) * time.Millisecond
}

// delaySourceLocked 延后站源，供内部持锁路径调用。
func (manager *nativeDownloads) delaySourceLocked(source string) {
	delay := nativeDownloadQueueDelay()
	if manager.queueDelay != nil {
		delay = manager.queueDelay()
	}
	if manager.sourceStarts == nil {
		manager.sourceStarts = map[string]time.Time{}
	}
	if next := time.Now().Add(delay); next.After(manager.sourceStarts[source]) {
		manager.sourceStarts[source] = next
	}
}

// sourceReadyLocked 返回站源就绪，供内部持锁路径调用。
func (manager *nativeDownloads) sourceReadyLocked(source string) time.Time {
	ready := manager.sourceStarts[source]
	if backoff := manager.sourceBackoffs[source]; backoff != nil && backoff.until.After(ready) {
		ready = backoff.until
	}
	return ready
}

// stopQueueTimerLocked 停止队列Timer，供内部持锁路径调用。
func (manager *nativeDownloads) stopQueueTimerLocked() {
	manager.queueGeneration++
	if manager.queueTimer != nil {
		manager.queueTimer.Stop()
		manager.queueTimer = nil
	}
}

// wakeQueueLocked 唤醒队列，供内部持锁路径调用。
func (manager *nativeDownloads) wakeQueueLocked(at time.Time) {
	if at.IsZero() || manager.closed || manager.moving {
		return
	}
	generation := manager.queueGeneration
	manager.queueTimer = time.AfterFunc(max(time.Millisecond, time.Until(at)), func() {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if generation != manager.queueGeneration {
			return
		}
		manager.queueTimer = nil
		manager.scheduleLocked()
	})
}

// recordSourceBackoffLocked 记录站源退避，供内部持锁路径调用。
func (manager *nativeDownloads) recordSourceBackoffLocked(source string, backoff *requestBackoff) {
	if source == "" || backoff == nil {
		return
	}
	if manager.sourceBackoffs == nil {
		manager.sourceBackoffs = map[string]*requestBackoff{}
	}
	previous := manager.sourceBackoffs[source]
	if previous == nil || backoff.until.After(previous.until) {
		copy := *backoff
		manager.sourceBackoffs[source] = &copy
	}
}

// downloadSourceBackoff 下载站源退避。
func (manager *nativeDownloads) downloadSourceBackoff(ctx context.Context) error {
	source, _ := ctx.Value(nativeDownloadSourceKey{}).(string)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if backoff := manager.sourceBackoffs[source]; backoff != nil && time.Now().Before(backoff.until) {
		copy := *backoff
		return &copy
	}
	return ctx.Err()
}

// observeDownloadBackoff 记录下载退避。
func (manager *nativeDownloads) observeDownloadBackoff(ctx context.Context, response *http.Response) *requestBackoff {
	header := response.Header.Get("Retry-After")
	challenge := strings.EqualFold(response.Header.Get("Cf-Mitigated"), "challenge")
	if response.StatusCode >= 200 && response.StatusCode < 300 && !challenge {
		return nil
	}
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusTooManyRequests && header == "" && !challenge {
		return nil
	}
	until := time.Now().Add(time.Minute)
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds >= 0 {
		until = time.Now().Add(time.Duration(seconds) * time.Second)
	} else if value, err := http.ParseTime(header); err == nil && value.After(time.Now()) {
		until = value
	}
	host := ""
	if response.Request != nil && response.Request.URL != nil {
		host = response.Request.URL.Hostname()
	}
	backoff := &requestBackoff{host: host, until: until, status: response.StatusCode}
	if challenge {
		backoff.reason = "站点要求浏览器验证，已暂停该站点后续请求"
	}
	source, _ := ctx.Value(nativeDownloadSourceKey{}).(string)
	manager.mu.Lock()
	manager.recordSourceBackoffLocked(source, backoff)
	manager.mu.Unlock()
	return backoff
}
