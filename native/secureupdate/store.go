package secureupdate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"duanjuapp/native/portable"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Install struct{ Root, Cache, Edition, ID, Key, Base string }
type Selection struct{ Active, Previous string }

// Locate 构造与当前安装目录和分发身份对应的更新存储位置。
func Locate(root, ed, id, key, base string) (Install, error) {
	root = filepath.Clean(root)
	data, e := portable.DataDirectory(root)
	if e != nil || !ValidEdition(ed) {
		return Install{}, errors.New("更新安装位置无效")
	}
	return Install{root, filepath.Join(data, "updates"), ed, id, key, base}, nil
}

// Init 初始化当前安装的更新缓存与状态目录。
func (i Install) Init() error {
	if e := portable.EnsureDirectory(i.Cache); e != nil {
		return e
	}
	return NoLinks(i.Cache)
}

// Random 生成更新会话使用的随机令牌。
func Random() string {
	var b [20]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

// ValidToken 检查更新会话令牌的格式。
func ValidToken(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 20 }

// atomicWrite 通过临时文件替换写入状态，避免留下半写入内容。
func atomicWrite(p string, b []byte) error {
	if e := NoLinks(filepath.Dir(p)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".write-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return replaceFile(f.Name(), p)
}

// Selection 读取当前启用与前一个版本的选择状态。
func (i Install) Selection() (v Selection) {
	b, e := small(filepath.Join(i.Cache, "active.json"), 2048)
	if e != nil || json.Unmarshal(b, &v) != nil {
		return Selection{}
	}
	return
}

// Commit 持久保存当前启用与前一个版本的选择状态。
func (i Install) Commit(v Selection) error {
	if (v.Active != "" && !ValidDigest(v.Active)) || (v.Previous != "" && !ValidDigest(v.Previous)) {
		return errors.New("更新版本指针无效")
	}
	b, _ := json.Marshal(v)
	return atomicWrite(filepath.Join(i.Cache, "active.json"), b)
}

// Slot 定位并验证指定摘要对应的版本目录。
func (i Install) Slot(sha string) (dir, version string, err error) {
	if sha == "" {
		return i.Root, i.Base, VerifyTree(i.Root, i.Key, i.Edition, i.ID, i.Base, false)
	}
	if !ValidDigest(sha) {
		return "", "", errors.New("版本目录无效")
	}
	slot := filepath.Join(i.Cache, "slot-"+sha)
	if err = NoLinks(slot); err != nil {
		return
	}
	b, e := small(filepath.Join(slot, "release.json"), 16384)
	if e != nil {
		return "", "", e
	}
	r, e := ReadRelease(b, i.Key, i.Edition, i.ID)
	if e != nil || r.SHA256 != sha || !Newer(r.Version, i.Base) {
		return "", "", errors.New("已缓存版本的签名、版本或摘要不符")
	}
	dir = filepath.Join(slot, "bundle")
	return dir, r.Version, VerifyTree(dir, i.Key, i.Edition, i.ID, r.Version, true)
}

// Request 保存当前更新会话申请切换的目标摘要。
func (i Install) Request(token, sha string) error {
	if !ValidToken(token) || !ValidDigest(sha) {
		return errors.New("更新启动上下文无效")
	}
	return atomicWrite(filepath.Join(i.Cache, "request-"+token), []byte(sha))
}

// Take 读取并消费指定更新会话的切换请求。
func (i Install) Take(token string) string {
	if !ValidToken(token) {
		return ""
	}
	p := filepath.Join(i.Cache, "request-"+token)
	b, e := small(p, 64)
	if e != nil || !ValidDigest(string(b)) {
		return ""
	}
	if e = os.Remove(p); e != nil {
		return ""
	}
	return string(b)
}

// Ack 写入当前更新会话的启动就绪确认。
func (i Install) Ack(token string) error {
	if !ValidToken(token) {
		return errors.New("启动确认无效")
	}
	return atomicWrite(filepath.Join(i.Cache, "ready-"+token), []byte("ready"))
}

// Ready 检查指定更新会话是否已经确认启动就绪。
func (i Install) Ready(token string) bool {
	if !ValidToken(token) {
		return false
	}
	b, e := small(filepath.Join(i.Cache, "ready-"+token), 16)
	return e == nil && string(b) == "ready"
}

// Stage 验证更新描述与整包，并准备可切换的版本目录。
func (i Install) Stage(zipPath string, body []byte, current string) (string, error) {
	r, e := ReadRelease(body, i.Key, i.Edition, i.ID)
	if e != nil {
		return "", e
	}
	if !Newer(r.Version, current) {
		return "", errors.New("拒绝安装相同或更旧的版本")
	}
	if e = i.Init(); e != nil {
		return "", e
	}
	tmp, e := os.MkdirTemp(i.Cache, ".prepare-")
	if e != nil {
		return "", e
	}
	defer func() {
		if filepath.IsAbs(tmp) && filepath.Dir(tmp) == filepath.Clean(i.Cache) && strings.HasPrefix(filepath.Base(tmp), ".prepare-") && NoLinks(tmp) == nil {
			_ = os.RemoveAll(tmp)
		}
	}()
	if e = Extract(zipPath, filepath.Join(tmp, "bundle"), i.Key, r); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(tmp, "release.json"), body, 0600); e != nil {
		return "", e
	}
	dst := filepath.Join(i.Cache, "slot-"+r.SHA256)
	if e = os.Rename(tmp, dst); e != nil {
		if _, _, v := i.Slot(r.SHA256); v != nil {
			return "", e
		}
	}
	return r.SHA256, nil
}

type Offer struct {
	Release          Release
	Envelope         []byte
	URL, Page, Notes string
}

// allowedURL 检查更新请求 URL 是否符合允许的协议和主机约束。
func allowedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	}
	return false
}

// fetch 以 GET 方法读取更新资源。
func fetch(ctx context.Context, c *http.Client, address string) (*http.Response, error) {
	return fetchMethod(ctx, c, http.MethodGet, address)
}

// fetchMethod 按给定 HTTP 方法读取更新资源。
func fetchMethod(ctx context.Context, c *http.Client, method, address string) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, method, address, nil)
	if e != nil || !allowedURL(r.URL) {
		return nil, errors.New("更新地址不受信任")
	}
	r.Header.Set("User-Agent", "GuoApp-Signed-Updater/1")
	if address == ReleaseFeedURL {
		r.Header.Set("Accept", "application/atom+xml, application/xml")
	}
	copy := *c
	copy.Jar = nil
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Referer")
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		if len(via) > 5 || !allowedURL(req.URL) || (address == ReleaseFeedURL || address == ReleasesURL) && req.URL.String() != address {
			return errPublicRedirect
		}
		return nil
	}
	resp, e := copy.Do(r)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		if until := backoff(resp.StatusCode, resp.Header, time.Now()); !until.IsZero() {
			return nil, &updateLimit{updateHTTPStatus(resp.StatusCode), until}
		}
		return nil, updateHTTPStatus(resp.StatusCode)
	}
	return resp, nil
}

// readWeb 按大小上限读取更新接口响应。
func readWeb(ctx context.Context, c *http.Client, u string, n int64) ([]byte, error) {
	r, e := fetch(ctx, c, u)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, n+1))
	if int64(len(b)) > n {
		return nil, errors.New("更新清单过大")
	}
	return b, e
}

type updateHTTPStatus int

// Error 返回当前错误的可显示文本。
func (s updateHTTPStatus) Error() string { return fmt.Sprintf("更新服务 HTTP %d", int(s)) }

// publicError 包装面向调用方的错误，避免直接暴露敏感请求信息。
func publicError(e error) error {
	var lim *updateLimit
	var code updateHTTPStatus
	if errors.As(e, &lim) {
		return lim
	}
	if errors.As(e, &code) {
		return code
	}
	var u *url.Error
	if errors.As(e, &u) {
		if u.Timeout() {
			return errors.New("连接 GitHub 超时，请检查本机网络或代理")
		}
		return errors.New("无法建立受信任的 GitHub 连接，请检查本机网络、证书或代理")
	}
	return e
}

var ErrNoPublishedRelease = errors.New("该仓库尚未发布可识别的更新版本，暂时无需更新。")

type PublishedRelease struct{ Tag string }

var tagVersionRE = regexp.MustCompile(`^tag(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// Version 返回发布条目对应的软件版本。
func (r PublishedRelease) Version() string {
	if tagVersionRE.MatchString(r.Tag) {
		return r.Tag[3:]
	}
	full := strings.TrimPrefix(r.Tag, "v")
	if verRE.MatchString(full) {
		return full
	}
	return ""
}

// comparePublished 比较已发布。
func comparePublished(a, b string) int {
	if a == "" || b == "" {
		return strings.Compare(a, b)
	}
	nums := func(v string) [4]int {
		var n [4]int
		for j, x := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '+' }) {
			if j < 4 {
				n[j], _ = strconv.Atoi(x)
			}
		}
		return n
	}
	x, y := nums(a), nums(b)
	for j := range x {
		if x[j] != y[j] {
			return x[j] - y[j]
		}
	}
	return 0
}

// NewerThan 判断发布条目是否高于当前版本。
func (r PublishedRelease) NewerThan(current string) bool {
	if !verRE.MatchString(current) {
		return false
	}
	if tagVersionRE.MatchString(r.Tag) {
		v, _, _ := strings.Cut(current, "+")
		return comparePublished(r.Version(), v) > 0
	}
	return Newer(r.Version(), current)
}

// matches 判断发布条目是否与签名版本相匹配。
func (r PublishedRelease) matches(signed string) bool {
	if tagVersionRE.MatchString(r.Tag) {
		v, _, _ := strings.Cut(signed, "+")
		return r.Version() == v
	}
	return r.Version() == signed
}

// Discover 发现并验证适用于当前软件与分发身份的更新。
func Discover(ctx context.Context, c *http.Client, ed, id, key, current string) (*Offer, error) {
	_, o, e := CheckRelease(ctx, c, ed, id, key, current)
	return o, e
}

// CheckRelease 检查发布渠道，并返回最新发布信息与可用更新。
func CheckRelease(ctx context.Context, c *http.Client, ed, id, key, current string) (PublishedRelease, *Offer, error) {
	if !ValidEdition(ed) {
		return PublishedRelease{}, nil, errors.New("软件身份无效")
	}
	api, e := LatestPublishedRelease(ctx, c)
	if e != nil {
		return PublishedRelease{}, nil, e
	}
	if !api.NewerThan(current) {
		return api, nil, nil
	}
	descriptor := ed + "-windows-x64.update.json"
	page := ReleasesURL + "/tag/" + url.PathEscape(api.Tag)
	base := ReleasesURL + "/download/" + url.PathEscape(api.Tag) + "/"
	body, e := readWeb(ctx, c, base+descriptor, 16384)
	if e != nil {
		if errors.Is(e, updateHTTPStatus(404)) {
			return api, nil, fmt.Errorf("发布 %s 缺少本款软件的签名清单 %s，未安装更新", api.Tag, descriptor)
		}
		return api, nil, fmt.Errorf("读取 %s 的签名清单：%w", ed, publicError(e))
	}
	r, e := ReadRelease(body, key, ed, id)
	if e != nil {
		return api, nil, e
	}
	if !api.matches(r.Version) || !Newer(r.Version, current) {
		return api, nil, errors.New("发布标签与签名版本不符")
	}
	address := base + url.PathEscape(r.Archive)
	return api, &Offer{r, body, address, page, ""}, nil
}

// Download 下载并校验更新整包，准备后续版本切换。
func (i Install) Download(ctx context.Context, c *http.Client, o Offer, current string, progress func(int64, int64)) (string, error) {
	r, e := ReadRelease(o.Envelope, i.Key, i.Edition, i.ID)
	if e != nil || !Newer(r.Version, current) {
		return "", errors.New("下载前签名或版本核对失败")
	}
	if e = i.Init(); e != nil {
		return "", e
	}
	resp, e := fetch(ctx, c, o.URL)
	if e != nil {
		return "", fmt.Errorf("下载 %s 更新包：%w", i.Edition, publicError(e))
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != r.Size {
		return "", errors.New("下载大小与签名清单不符")
	}
	f, e := os.CreateTemp(i.Cache, ".archive-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	w := io.MultiWriter(f, h)
	buf := make([]byte, 128<<10)
	var n int64
	for {
		count, err := resp.Body.Read(buf)
		n += int64(count)
		if n > r.Size {
			f.Close()
			return "", errors.New("下载内容超出签名大小")
		}
		if count > 0 {
			if _, e = w.Write(buf[:count]); e != nil {
				f.Close()
				return "", e
			}
			progress(n, r.Size)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return "", err
		}
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return "", errors.New("更新包 SHA-256 不符，保留旧版")
	}
	progress(-1, r.Size)
	return i.Stage(f.Name(), o.Envelope, current)
}
