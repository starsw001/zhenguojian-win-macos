package secureupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUpdateChildFixture 验证「Update Child Fixture」回归场景。
func TestUpdateChildFixture(t *testing.T) {
	if os.Getenv("GUO_FIXTURE_CHILD") != "yes" {
		t.Skip("isolated subprocess only")
	}
	i := Install{Root: os.Getenv("GUO_UPDATE_ROOT"), Cache: os.Getenv("GUO_FIXTURE_CACHE")}
	session := os.Getenv("GUO_UPDATE_SESSION")
	if os.Getenv("GUO_UPDATE_TRIAL") == "1" {
		if os.Getenv("GUO_FIXTURE_FAIL") == "yes" {
			os.Exit(15)
		}
		if i.Ack(session) != nil {
			os.Exit(3)
		}
		for n := 0; n < 200; n++ {
			if i.Selection().Active == os.Getenv("GUO_FIXTURE_SLOT") {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(4)
	}
	first, e := os.OpenFile(filepath.Join(i.Cache, "asked"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		os.Exit(0)
	}
	if e != nil {
		os.Exit(5)
	}
	first.Close()
	if i.Request(session, os.Getenv("GUO_FIXTURE_SLOT")) != nil {
		os.Exit(6)
	}
	os.Exit(RestartCode)
}

// TestTwoWindowsProcessesUpgradeAndFailback 验证「Two Windows Processes Upgrade And Failback」回归场景。
func TestTwoWindowsProcessesUpgradeAndFailback(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	app, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	for _, ed := range []string{"hongguoshang", "zhenguoshang"} {
		for _, fail := range []bool{false, true} {
			test := ed + "-success"
			if fail {
				test = ed + "-trial-crash"
			}
			t.Run(test, func(t *testing.T) {
				f := newFixture(t, ed, app)
				r, meta, _ := f.offer(t, app)
				slot, e := f.i.Stage(filepath.Join(f.releaseDir, r.Archive), meta, f.i.Base)
				if e != nil {
					t.Fatal(e)
				}
				for _, p := range []string{"download/exports/existing.mp4", "logs/app.log", "fixture-profile/watch.json", "fixture-profile/license-state.dat"} {
					full := filepath.Join(f.i.Root, p)
					os.MkdirAll(filepath.Dir(full), 0700)
					os.WriteFile(full, []byte("do-not-change"), 0600)
				}
				t.Setenv("GUO_FIXTURE_CHILD", "yes")
				t.Setenv("GUO_FIXTURE_CACHE", f.i.Cache)
				t.Setenv("GUO_FIXTURE_SLOT", slot)
				if fail {
					t.Setenv("GUO_FIXTURE_FAIL", "yes")
				}
				notices := 0
				err := f.i.Supervise(f.i.Start([]string{"-test.run=^TestUpdateChildFixture$"}), 5*time.Second, func(string) { notices++ })
				if err != nil {
					t.Fatal(err)
				}
				if fail && (notices != 1 || f.i.Selection().Active != "") {
					t.Fatal("factory not restored")
				}
				if !fail && (notices != 0 || f.i.Selection().Active != slot) {
					t.Fatal("new child did not commit")
				}
				for _, p := range []string{"download/exports/existing.mp4", "logs/app.log", "fixture-profile/watch.json", "fixture-profile/license-state.dat"} {
					b, e := os.ReadFile(filepath.Join(f.i.Root, p))
					if e != nil || string(b) != "do-not-change" {
						t.Fatal("sentinel changed")
					}
				}
				if e = VerifyTree(f.i.Root, f.i.Key, ed, f.i.ID, f.i.Base, false); e != nil {
					t.Fatal("old executable changed")
				}
			})
		}
	}
}

// TestRejectDamagedActiveSlot 验证「Reject Damaged Active Slot」回归场景。
func TestRejectDamagedActiveSlot(t *testing.T) {
	f := newFixture(t, "hongguoshang", nil)
	if e := f.i.Commit(Selection{Active: strings.Repeat("a", 64)}); e != nil {
		t.Fatal(e)
	}
	ran := false
	e := f.i.Supervise(func(dir, token string, trial bool) (Child, error) {
		if dir != f.i.Root {
			t.Fatal("attempt to execute invalid slot")
		}
		ran = true
		p := &fakeChild{make(chan int, 1)}
		p.done <- 0
		return p, nil
	}, time.Second, func(string) {})
	if e != nil || !ran || f.i.Selection().Active != "" {
		t.Fatal(e)
	}
}
