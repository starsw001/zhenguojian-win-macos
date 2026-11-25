package secureupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	i               Install
	priv            ed25519.PrivateKey
	key, releaseDir string
}

// env 返回env。
func env(b []byte, p ed25519.PrivateKey) []byte {
	v, _ := json.Marshal(Envelope{base64.RawURLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(ed25519.Sign(p, b))})
	return v
}

// digest 计算摘要：updatetest。
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

// makeTree 构造Tree。
func makeTree(t *testing.T, dir, ed, id, v string, priv ed25519.PrivateKey, app []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Join(dir, "data"), 0700); e != nil {
		t.Fatal(e)
	}
	m := Manifest{Edition: ed, Version: v, Protocol: "guoapp-license-v1", DistributionID: id, Category: 1, AccountMode: "universal", Files: map[string]string{}}
	for _, n := range []string{ed + ".exe", "guo_window.dll", "flutter_windows.dll", "duanju_core.dll", "data/app.so"} {
		b := []byte(n + v)
		if n == ed+".exe" && app != nil {
			b = app
		}
		if e := os.WriteFile(filepath.Join(dir, n), b, 0700); e != nil {
			t.Fatal(e)
		}
		m.Files[n] = digest(b)
	}
	b, _ := json.Marshal(m)
	if e := os.WriteFile(filepath.Join(dir, "distribution.json"), env(b, priv), 0600); e != nil {
		t.Fatal(e)
	}
}

// newFixture 创建Fixture。
func newFixture(t *testing.T, ed string, app []byte) fixture {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	i := Install{Root: t.TempDir(), Cache: t.TempDir(), Edition: ed, Key: base64.RawURLEncoding.EncodeToString(pub), ID: "fixture-158", Base: "1.1.27+158"}
	key := filepath.Join(t.TempDir(), "ephemeral.key")
	os.WriteFile(key, []byte(base64.RawURLEncoding.EncodeToString(priv)), 0600)
	makeTree(t, i.Root, ed, i.ID, i.Base, priv, app)
	return fixture{i, priv, key, t.TempDir()}
}

// offer 返回offer。
func (f fixture) offer(t *testing.T, app []byte) (Release, []byte, []byte) {
	t.Helper()
	p := t.TempDir()
	v := "1.1.28+159"
	makeTree(t, p, f.i.Edition, f.i.ID, v, f.priv, app)
	os.WriteFile(filepath.Join(p, "github-update-url.txt"), []byte("not-distributed"), 0600)
	os.Mkdir(filepath.Join(p, "download"), 0700)
	os.WriteFile(filepath.Join(p, "download", "existing.mp4"), []byte("not-distributed"), 0600)
	r, e := Publish(p, f.releaseDir, f.key, f.i.Key, f.i.Edition, f.i.ID, v)
	if e != nil {
		t.Fatal(e)
	}
	meta, _ := os.ReadFile(filepath.Join(f.releaseDir, f.i.Edition+"-windows-x64.update.json"))
	b, _ := os.ReadFile(filepath.Join(f.releaseDir, r.Archive))
	return r, meta, b
}

func TestPublishedArchiveUsesVersionFolder(t *testing.T) {
	for _, edition := range []string{"hongguoshang", "zhenguoshang"} {
		f := newFixture(t, edition, nil)
		r, _, _ := f.offer(t, nil)
		z, err := zip.OpenReader(filepath.Join(f.releaseDir, r.Archive))
		if err != nil {
			t.Fatal(err)
		}
		root := ArchiveRoot(edition, r.Version)
		var flat bytes.Buffer
		flatWriter := zip.NewWriter(&flat)
		for _, entry := range z.File {
			if !strings.HasPrefix(entry.Name, root+"/") {
				z.Close()
				t.Fatalf("archive entry escaped version folder: %s", entry.Name)
			}
			name, layout, valid := archiveEntryName(entry.Name, r)
			if !valid || layout != layoutNested || !allowed(name, edition) {
				z.Close()
				t.Fatalf("invalid nested archive entry: %s", entry.Name)
			}
			writer, _ := flatWriter.Create(name)
			reader, _ := entry.Open()
			io.Copy(writer, reader)
			reader.Close()
		}
		flatWriter.Close()
		z.Close()
		legacy := r
		legacy.Size = int64(flat.Len())
		legacy.SHA256 = digest(flat.Bytes())
		legacyPath := filepath.Join(t.TempDir(), "legacy-flat.zip")
		os.WriteFile(legacyPath, flat.Bytes(), 0600)
		if err := Extract(legacyPath, filepath.Join(t.TempDir(), "legacy-flat"), f.i.Key, legacy); err != nil {
			t.Fatal("legacy flat archive rejected", err)
		}
	}
}

type transport func(*http.Request) (*http.Response, error)

// RoundTrip 实现隔离测试使用的单次 HTTP 请求处理。
func (fn transport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// TestPublisherDiscoveryDownload 验证「Publisher Discovery Download」回归场景。
func TestPublisherDiscoveryDownload(t *testing.T) {
	for _, ed := range []string{"hongguoshang", "zhenguoshang"} {
		t.Run(ed, func(t *testing.T) {
			f := newFixture(t, ed, nil)
			r, meta, b := f.offer(t, nil)
			c := &http.Client{Transport: transport(func(q *http.Request) (*http.Response, error) {
				var data []byte
				tag := "tag" + strings.Split(r.Version, "+")[0]
				if q.URL.String() == ReleaseFeedURL {
					if q.Method != http.MethodGet {
						t.Fatal("expected a public release list request")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(testAtom(tag)))}, nil
				}
				if q.Method != http.MethodGet {
					t.Fatal("checking must not require HEAD on release assets")
				}
				if !strings.HasPrefix(q.URL.String(), ReleasesURL+"/download/"+tag+"/") {
					t.Fatalf("wrong release asset channel: %s", q.URL)
				}
				switch {
				case strings.HasSuffix(q.URL.Path, ".json"):
					data = meta
				default:
					data = b
				}
				return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			o, e := Discover(context.Background(), c, ed, f.i.ID, f.i.Key, f.i.Base)
			if e != nil || o == nil {
				t.Fatal(e)
			}
			slot, e := f.i.Download(context.Background(), c, *o, f.i.Base, func(int64, int64) {})
			if e != nil || slot != r.SHA256 {
				t.Fatal(e)
			}
			dir, v, e := f.i.Slot(slot)
			if e != nil || v != r.Version {
				t.Fatal(e)
			}
			for _, p := range []string{"download", "github-update-url.txt"} {
				if _, e = os.Stat(filepath.Join(dir, p)); !os.IsNotExist(e) {
					t.Fatal("non-runtime entry published")
				}
			}
			if f.i.Selection().Active != "" {
				t.Fatal("staging selected version prematurely")
			}
			if _, e = f.i.Stage(filepath.Join(f.releaseDir, r.Archive), meta, r.Version); e == nil {
				t.Fatal("version replay")
			}
			b[len(b)/2] ^= 1
			os.WriteFile(filepath.Join(f.releaseDir, r.Archive), b, 0600)
			if _, e = f.i.Stage(filepath.Join(f.releaseDir, r.Archive), meta, f.i.Base); e == nil {
				t.Fatal("tampered archive")
			}
			if e = VerifyTree(f.i.Root, f.i.Key, ed, f.i.ID, f.i.Base, false); e != nil {
				t.Fatal("old release touched")
			}
		})
	}
}

// TestSignatureAndIdentity 验证「Signature And Identity」回归场景。
func TestSignatureAndIdentity(t *testing.T) {
	f := newFixture(t, "hongguoshang", nil)
	r, meta, _ := f.offer(t, nil)
	if _, e := ReadRelease(meta, f.i.Key, "zhenguoshang", f.i.ID); e == nil {
		t.Fatal("wrong edition")
	}
	if _, e := ReadRelease(meta, f.i.Key, r.Edition, "different-id"); e == nil {
		t.Fatal("wrong distribution")
	}
	var v Envelope
	json.Unmarshal(meta, &v)
	v.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	bad, _ := json.Marshal(v)
	if _, e := ReadRelease(bad, f.i.Key, r.Edition, f.i.ID); e == nil {
		t.Fatal("forgery")
	}
	for _, change := range []func(*Release){func(x *Release) { x.Platform = "windows-arm64" }, func(x *Release) { x.Protocol = "other" }, func(x *Release) { x.Archive = "../other.zip" }, func(x *Release) { x.Size = MaxArchive + 1 }} {
		rr := r
		change(&rr)
		b, _ := json.Marshal(rr)
		if _, e := ReadRelease(env(b, f.priv), f.i.Key, r.Edition, f.i.ID); e == nil {
			t.Fatal("invalid metadata")
		}
	}
	for _, s := range []string{"1.1.26+159", "1.1.28+157", "1.1.27+158", "v1.9.0+190"} {
		if Newer(s, f.i.Base) {
			t.Fatal(s)
		}
	}
}

// TestRejectUnsafeArchive 验证「Reject Unsafe Archive」回归场景。
func TestRejectUnsafeArchive(t *testing.T) {
	f := newFixture(t, "hongguoshang", nil)
	r, _, good := f.offer(t, nil)
	for _, name := range []string{"../escape", "data/../x", "download/history.json", "logs/app.log", "C:/escape", "data/x:stream", "DATA/APP.SO", "data/link", "data/unknown", "bad-inner-hash", "missing-file"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			var buf bytes.Buffer
			w := zip.NewWriter(&buf)
			z, _ := zip.NewReader(bytes.NewReader(good), int64(len(good)))
			for _, entry := range z.File {
				inner, _, _ := archiveEntryName(entry.Name, r)
				if name == "missing-file" && inner == "data/app.so" {
					continue
				}
				o, _ := w.Create(entry.Name)
				in, _ := entry.Open()
				if name == "bad-inner-hash" && inner == "data/app.so" {
					o.Write([]byte("changed"))
				} else {
					io.Copy(o, in)
				}
				in.Close()
			}
			if name != "bad-inner-hash" && name != "missing-file" {
				hdr := zip.FileHeader{Name: ArchiveRoot(r.Edition, r.Version) + "/" + name}
				if name == "data/link" {
					hdr.SetMode(os.ModeSymlink | 0777)
				}
				q, _ := w.CreateHeader(&hdr)
				q.Write([]byte("x"))
			}
			w.Close()
			b := buf.Bytes()
			rr := r
			rr.Size = int64(len(b))
			rr.SHA256 = digest(b)
			mb, _ := json.Marshal(rr)
			path := filepath.Join(t.TempDir(), "new.zip")
			os.WriteFile(path, b, 0600)
			if _, e := f.i.Stage(path, env(mb, f.priv), f.i.Base); e == nil {
				t.Fatal("unsafe archive allowed")
			}
			if f.i.Selection().Active != "" {
				t.Fatal("old selection changed")
			}
		})
	}
}

type fakeChild struct{ done chan int }

// Done 返回Done。
func (f *fakeChild) Done() <-chan int { return f.done }

// Stop 停止fakeChild。
func (f *fakeChild) Stop() { f.done <- 9 }

// TestTrialFallback 验证「Trial Fallback」回归场景。
func TestTrialFallback(t *testing.T) {
	for _, failure := range []string{"cannot-start", "early-exit", "no-ack", "good"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t, "hongguoshang", nil)
			r, meta, _ := f.offer(t, nil)
			slot, e := f.i.Stage(filepath.Join(f.releaseDir, r.Archive), meta, f.i.Base)
			if e != nil {
				t.Fatal(e)
			}
			runs := 0
			notices := 0
			e = f.i.Supervise(func(dir, token string, trial bool) (Child, error) {
				runs++
				p := &fakeChild{make(chan int, 1)}
				if runs == 1 {
					if trial || dir != f.i.Root {
						t.Fatal("not base")
					}
					if e := f.i.Request(token, slot); e != nil {
						t.Fatal(e)
					}
					p.done <- RestartCode
				} else if trial {
					switch failure {
					case "cannot-start":
						return nil, errors.New("test failure")
					case "early-exit":
						p.done <- 2
					case "good":
						f.i.Ack(token)
						go func() {
							for n := 0; n < 200; n++ {
								if f.i.Selection().Active == slot {
									p.done <- 0
									return
								}
								time.Sleep(5 * time.Millisecond)
							}
							p.done <- 2
						}()
					}
				} else {
					if dir != f.i.Root {
						t.Fatal("not original runtime")
					}
					p.done <- 0
				}
				return p, nil
			}, 300*time.Millisecond, func(string) { notices++ })
			if e != nil {
				t.Fatal(e)
			}
			if failure == "good" {
				if runs != 2 || f.i.Selection().Active != slot {
					t.Fatal("not committed")
				}
			} else if runs != 3 || notices == 0 || f.i.Selection().Active != "" {
				t.Fatal("rollback failed", runs, notices)
			}
		})
	}
}

// TestOnlyOneBrandExecutable 验证「Only One Brand Executable」回归场景。
func TestOnlyOneBrandExecutable(t *testing.T) {
	for _, ed := range []string{"hongguoshang", "zhenguoshang"} {
		if !allowed(ed+".exe", ed) || !allowed("guo_window.dll", ed) {
			t.Fatal("missing signed entry or window host:", ed)
		}
		for _, other := range []string{ed + ".runtime.exe", "other.exe", "data/OTHER.ExE"} {
			if allowed(other, ed) {
				t.Fatal("unexpected second executable:", other)
			}
		}
	}
}
