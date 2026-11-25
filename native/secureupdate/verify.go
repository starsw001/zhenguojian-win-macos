package secureupdate

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"duanjuapp/native/portable"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const Protocol = "guoapp-windows-update-v1"
const MaxArchive = int64(768 << 20)
const MaxExtract = int64(1536 << 20)
const RestartCode = 74

const Repository = "starsw001/hongguo-win-public-package"
const ReleasesURL = "https://github.com/" + Repository + "/releases"

var digestRE = regexp.MustCompile("^[a-f0-9]{64}$")
var verRE = regexp.MustCompile("^(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})\\.(0|[1-9][0-9]{0,8})\\+([1-9][0-9]{0,8})$")

type Envelope struct{ Payload, Signature string }
type Release struct {
	Protocol       string `json:"protocol"`
	Edition        string `json:"edition"`
	Platform       string `json:"platform"`
	Version        string `json:"version"`
	DistributionID string `json:"distributionId"`
	Archive        string `json:"archive"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
}
type Manifest struct {
	Protocol, Edition, Version, DistributionID, AccountMode string
	Category                                                int
	Files                                                   map[string]string
}

// ArchiveName 按软件标识和版本构造更新整包文件名。
func ArchiveName(ed, v string) string {
	return ed + "-" + strings.ReplaceAll(v, "+", "-build") + "-windows-x64.zip"
}

func ArchiveRoot(ed, v string) string {
	return strings.TrimSuffix(ArchiveName(ed, v), ".zip")
}

// ValidEdition 判断软件标识是否属于允许的版本类型。
func ValidEdition(ed string) bool { return ed == "hongguoshang" || ed == "zhenguoshang" }

// ValidDigest 检查整包摘要是否符合 SHA-256 文本格式。
func ValidDigest(s string) bool { return digestRE.MatchString(s) }

// Newer 比较版本号，判断候选版本是否更新。
func Newer(next, old string) bool {
	a, b := verRE.FindStringSubmatch(next), verRE.FindStringSubmatch(old)
	if a == nil || b == nil {
		return false
	}
	n, _ := strconv.Atoi(a[4])
	o, _ := strconv.Atoi(b[4])
	if n <= o {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return true
}

// small 在大小上限内读取文件内容。
func small(p string, n int64) ([]byte, error) {
	i, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > n {
		return nil, errors.New("更新文件无效或过大")
	}
	return os.ReadFile(p)
}

// verify 验证 Ed25519 签名信封并解码载荷。
func verify(body []byte, key string, out any) error {
	var env Envelope
	pub, e := base64.RawURLEncoding.DecodeString(key)
	if e != nil || len(pub) != ed25519.PublicKeySize || json.Unmarshal(body, &env) != nil {
		return errors.New("缺少可信发布签名")
	}
	b, e := base64.RawURLEncoding.DecodeString(env.Payload)
	sig, se := base64.RawURLEncoding.DecodeString(env.Signature)
	if e != nil || se != nil || !ed25519.Verify(pub, b, sig) {
		return errors.New("发布签名无效，未安装")
	}
	return json.Unmarshal(b, out)
}

// ReadRelease 验证更新签名并校验软件与分发身份。
func ReadRelease(body []byte, key, ed, id string) (Release, error) {
	var r Release
	if e := verify(body, key, &r); e != nil {
		return r, e
	}
	if !ValidEdition(ed) || r.Edition != ed || r.DistributionID == "" || r.DistributionID != id || r.Protocol != Protocol || r.Platform != "windows-x64" || !verRE.MatchString(r.Version) || r.Archive != ArchiveName(ed, r.Version) || !ValidDigest(r.SHA256) || r.Size <= 0 || r.Size > MaxArchive {
		return r, errors.New("更新清单中的软件、分发、版本或整包信息不匹配")
	}
	return r, nil
}

// safeName 检查更新包条目是否为安全的相对文件名。
func safeName(s string) bool {
	if s == "" || len(s) > 250 || !filepath.IsLocal(filepath.FromSlash(s)) || strings.ContainsAny(s, "\\:%<>|\"?*") {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") || strings.HasPrefix(p, ".") {
			return false
		}
		for _, c := range p {
			if c < 32 || c == 127 {
				return false
			}
		}
	}
	return true
}

// allowed 检查文件是否属于指定软件版本允许的运行文件。
func allowed(s, ed string) bool {
	if strings.HasSuffix(strings.ToLower(s), ".exe") {
		return s == ed+".exe"
	}
	return safeName(s) && (s == "native_assets.json" || s == "distribution.json" || strings.HasPrefix(s, "data/") || (!strings.Contains(s, "/") && strings.HasSuffix(s, ".dll")))
}

const (
	layoutUnknown = iota
	layoutFlat
	layoutNested
)

func archiveEntryName(name string, r Release) (string, int, bool) {
	prefix := ArchiveRoot(r.Edition, r.Version) + "/"
	if strings.HasPrefix(name, prefix) {
		inner := strings.TrimPrefix(name, prefix)
		return inner, layoutNested, inner != ""
	}
	return name, layoutFlat, name != ""
}

// getManifest 验证分发清单签名并核对软件、版本与分发身份。
func getManifest(body []byte, key, ed, id, v string) (Manifest, error) {
	var m Manifest
	if e := verify(body, key, &m); e != nil {
		return m, e
	}
	if !ValidEdition(ed) || m.Edition != ed || m.Version != v || !verRE.MatchString(v) || m.Protocol != "guoapp-license-v1" || m.DistributionID != id || id == "" || m.AccountMode != "universal" || m.Category != 1 || len(m.Files) > 4096 {
		return m, errors.New("软件包与当前授权分发不符")
	}
	seen := map[string]bool{}
	for s, h := range m.Files {
		l := strings.ToLower(s)
		if !allowed(s, ed) || s == "distribution.json" || seen[l] || !ValidDigest(h) {
			return m, errors.New("签名运行文件清单无效")
		}
		seen[l] = true
	}
	for _, n := range []string{ed + ".exe", "guo_window.dll", "duanju_core.dll", "flutter_windows.dll", "data/app.so"} {
		if m.Files[n] == "" {
			return m, errors.New("缺少签名运行组件")
		}
	}
	return m, nil
}

// hashFile 读取文件并计算 SHA-256 摘要。
func hashFile(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}

// NoLinks 检查目标路径及其父路径，拒绝符号链接。
func NoLinks(p string) error {
	p = filepath.Clean(p)
	for {
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("不使用含链接的更新路径")
		}
		next := filepath.Dir(p)
		if next == p {
			return nil
		}
		p = next
	}
}

// VerifyTree 验证解压目录的签名清单、软件身份及文件哈希。
func VerifyTree(dir, key, edition, id, version string, strict bool) error {
	p := filepath.Join(dir, "distribution.json")
	if e := NoLinks(p); e != nil {
		return e
	}
	body, e := small(p, 512<<10)
	if e != nil {
		return e
	}
	m, e := getManifest(body, key, edition, id, version)
	if e != nil {
		return e
	}
	for name, h := range m.Files {
		p = filepath.Join(dir, filepath.FromSlash(name))
		if e := NoLinks(p); e != nil {
			return e
		}
		s, e := os.Stat(p)
		if e != nil || !s.Mode().IsRegular() {
			return errors.New("运行文件缺失")
		}
		got, e := hashFile(p)
		if e != nil || got != h {
			return fmt.Errorf("运行文件验证失败：%s", name)
		}
	}
	if strict {
		return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("安装包包含链接")
			}
			if d.IsDir() {
				if filepath.Dir(path) == filepath.Clean(dir) && portable.MutableDirectory(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, e := filepath.Rel(dir, path)
			if e != nil {
				return e
			}
			s := filepath.ToSlash(rel)
			if s != "distribution.json" && m.Files[s] == "" {
				return errors.New("安装包有清单外文件")
			}
			return nil
		})
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, d := range entries {
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if (ext == ".dll" || ext == ".exe") && m.Files[d.Name()] == "" {
			return errors.New("目录有签名清单外的运行模块")
		}
	}
	return nil
}

// Extract 受限解压更新 ZIP，并校验目录结构、签名和文件哈希。
func Extract(zipPath, target, key string, r Release) error {
	stat, e := os.Lstat(zipPath)
	if e != nil || !stat.Mode().IsRegular() || stat.Size() != r.Size {
		return errors.New("更新包大小不符")
	}
	h, e := hashFile(zipPath)
	if e != nil || h != r.SHA256 {
		return errors.New("整包 SHA-256 不符，保留旧版")
	}
	z, e := zip.OpenReader(zipPath)
	if e != nil {
		return e
	}
	defer z.Close()
	if len(z.File) > 4097 {
		return errors.New("更新包含过多文件")
	}
	var manifest []byte
	seen := map[string]bool{}
	var total uint64
	layout := layoutUnknown
	for _, f := range z.File {
		n, currentLayout, valid := archiveEntryName(f.Name, r)
		l := strings.ToLower(n)
		if !valid || layout != layoutUnknown && layout != currentLayout || !allowed(n, r.Edition) || seen[l] || !f.Mode().IsRegular() || f.UncompressedSize64 > uint64(MaxArchive) || total+f.UncompressedSize64 > uint64(MaxExtract) {
			return errors.New("更新包路径、大小或条目类型异常")
		}
		layout = currentLayout
		total += f.UncompressedSize64
		seen[l] = true
		if n == "distribution.json" {
			if f.UncompressedSize64 > 512<<10 {
				return errors.New("签名清单过大")
			}
			s, e := f.Open()
			if e != nil {
				return e
			}
			manifest, e = io.ReadAll(io.LimitReader(s, (512<<10)+1))
			s.Close()
			if e != nil {
				return e
			}
		}
	}
	m, e := getManifest(manifest, key, r.Edition, r.DistributionID, r.Version)
	if e != nil {
		return e
	}
	if len(z.File) != len(m.Files)+1 {
		return errors.New("压缩包与签名清单不一致")
	}
	if e = os.Mkdir(target, 0700); e != nil {
		return e
	}
	for _, f := range z.File {
		name, _, valid := archiveEntryName(f.Name, r)
		if !valid || name != "distribution.json" && m.Files[name] == "" {
			return errors.New("压缩包包含未签名文件")
		}
		p := filepath.Join(target, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return e
		}
		if e = NoLinks(filepath.Dir(p)); e != nil {
			return e
		}
		s, e := f.Open()
		if e != nil {
			return e
		}
		w, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			s.Close()
			return e
		}
		n, ce := io.Copy(w, io.LimitReader(s, int64(f.UncompressedSize64)+1))
		se := s.Close()
		we := w.Close()
		if ce != nil || se != nil || we != nil || n != int64(f.UncompressedSize64) {
			return errors.New("解包不完整")
		}
	}
	return VerifyTree(target, key, r.Edition, r.DistributionID, r.Version, true)
}
