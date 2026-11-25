package secureupdate

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Publish 为指定软件目录生成更新 ZIP 与签名发布清单。
func Publish(dir, out, keyfile, expected, edition, id, version string) (Release, error) {
	var r Release
	raw, e := small(keyfile, 512)
	if e != nil {
		return r, e
	}
	key, e := base64.RawURLEncoding.DecodeString(trim(raw))
	if e != nil || len(key) != ed25519.PrivateKeySize {
		return r, errors.New("发布私钥无效")
	}
	priv := ed25519.PrivateKey(key)
	if base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)) != expected {
		return r, errors.New("分发公钥与私钥不一致")
	}
	if e = VerifyTree(dir, expected, edition, id, version, false); e != nil {
		return r, e
	}
	body, e := small(filepath.Join(dir, "distribution.json"), 512<<10)
	if e != nil {
		return r, e
	}
	m, e := getManifest(body, expected, edition, id, version)
	if e != nil {
		return r, e
	}
	if e = os.MkdirAll(out, 0700); e != nil {
		return r, e
	}
	archive := ArchiveName(edition, version)
	dst := filepath.Join(out, archive)
	meta := filepath.Join(out, edition+"-windows-x64.update.json")
	for _, p := range []string{dst, meta} {
		if _, e = os.Lstat(p); !os.IsNotExist(e) {
			return r, errors.New("发布文件已存在，不覆盖历史版本")
		}
	}
	tmp, e := os.CreateTemp(out, ".archive-")
	if e != nil {
		return r, e
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	z := zip.NewWriter(io.MultiWriter(tmp, h))
	archiveRoot := ArchiveRoot(edition, version)
	names := []string{"distribution.json"}
	for k := range m.Files {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, s := range names {
		w, e := z.Create(archiveRoot + "/" + s)
		if e != nil {
			return r, e
		}
		if s == "distribution.json" {
			if _, e = w.Write(body); e != nil {
				return r, e
			}
			continue
		}
		f, e := os.Open(filepath.Join(dir, filepath.FromSlash(s)))
		if e != nil {
			return r, e
		}
		fh := sha256.New()
		_, e = io.Copy(io.MultiWriter(w, fh), f)
		f.Close()
		if e != nil || hex.EncodeToString(fh.Sum(nil)) != m.Files[s] {
			return r, errors.New("打包期间运行文件发生变化")
		}
	}
	if e = z.Close(); e != nil {
		return r, e
	}
	if e = tmp.Sync(); e != nil {
		return r, e
	}
	st, e := tmp.Stat()
	if e != nil {
		return r, e
	}
	if e = tmp.Close(); e != nil {
		return r, e
	}
	r = Release{Protocol, edition, "windows-x64", version, id, archive, hex.EncodeToString(h.Sum(nil)), st.Size()}
	b, _ := json.Marshal(r)
	env, _ := json.Marshal(map[string]string{"payload": base64.RawURLEncoding.EncodeToString(b), "signature": base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, b))})
	if _, e = ReadRelease(env, expected, edition, id); e != nil {
		return r, e
	}
	if e = os.Link(tmp.Name(), dst); e != nil {
		return r, e
	}
	outf, e := os.OpenFile(meta, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		os.Remove(dst)
		return r, e
	}
	_, e = outf.Write(env)
	ce := outf.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		os.Remove(meta)
		os.Remove(dst)
	}
	return r, e
}

// trim 将字节文本转换为字符串并去除首尾空白。
func trim(b []byte) string {
	for len(b) > 0 && (b[len(b)-1] == '\r' || b[len(b)-1] == '\n' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return string(b)
}
