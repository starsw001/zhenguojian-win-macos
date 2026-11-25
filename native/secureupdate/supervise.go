package secureupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runtime 读取启动器环境并验证当前安装的更新身份。
func Runtime(key, ed string) (Install, error) {
	root := os.Getenv("GUO_UPDATE_ROOT")
	if !filepath.IsAbs(root) || !ValidToken(os.Getenv("GUO_UPDATE_SESSION")) {
		return Install{}, errors.New("请从原目录的软件主 EXE 启动，再进行更新")
	}
	b, e := small(filepath.Join(root, "distribution.json"), 512<<10)
	if e != nil {
		return Install{}, e
	}
	var m Manifest
	if e = verify(b, key, &m); e != nil {
		return Install{}, e
	}
	if _, e = getManifest(b, key, ed, m.DistributionID, m.Version); e != nil {
		return Install{}, e
	}
	return Locate(root, ed, m.DistributionID, key, m.Version)
}

type Child interface {
	Done() <-chan int
	Stop()
}
type StartChild func(dir, token string, trial bool) (Child, error)

// Supervise 监督版本切换与启动确认，在更新启动失败时回到已保留版本。
func (i Install) Supervise(start StartChild, timeout time.Duration, notice func(string)) error {
	if e := i.Init(); e != nil {
		dir, _, err := i.Slot("")
		if err != nil {
			return err
		}
		c, err := start(dir, Random(), false)
		if err != nil {
			return err
		}
		<-c.Done()
		return nil
	}
	state := i.Selection()
	current := state.Active
	var e error
	if current != "" {
		_, _, e = i.Slot(current)
	}
	if e != nil {
		current = state.Previous
		if _, _, e = i.Slot(current); e != nil {
			current = ""
		}
		_ = i.Commit(Selection{Active: current})
		notice("当前版本未通过校验，已回到可用旧版。")
	}
	target := current
	trial := false
	for {
		dir, version, err := i.Slot(target)
		if err == nil && trial {
			_, oldVersion, e := i.Slot(current)
			if e != nil || !Newer(version, oldVersion) {
				err = errors.New("更新目标不是更高版本")
			}
		}
		token := Random()
		var child Child
		if err == nil {
			child, err = start(dir, token, trial)
		}
		if err != nil {
			if !trial {
				return err
			}
			target = current
			trial = false
			notice("新版无法启动，旧版已保留。")
			continue
		}
		done := child.Done()
		if trial {
			deadline := time.NewTimer(timeout)
			tick := time.NewTicker(100 * time.Millisecond)
			ok := false
			exited := false
		wait:
			for {
				select {
				case <-done:
					exited = true
					break wait
				case <-deadline.C:
					break wait
				case <-tick.C:
					if i.Ready(token) {
						ok = true
						break wait
					}
				}
			}
			deadline.Stop()
			tick.Stop()
			os.Remove(filepath.Join(i.Cache, "ready-"+token))
			if ok {
				select {
				case <-done:
					ok = false
					exited = true
				default:
				}
			}
			if ok {
				err = i.Commit(Selection{Active: target, Previous: current})
				ok = err == nil
			}
			if !ok {
				if !exited {
					child.Stop()
					<-done
				}
				notice("新版启动未确认，已保留并重新打开旧版。")
				target = current
				trial = false
				continue
			}
			current = target
			trial = false
		}
		code := <-done
		if code != RestartCode {
			if code != 0 && current != "" {
				old := i.Selection()
				if old.Active == current && old.Previous != current {
					if _, _, e := i.Slot(old.Previous); e == nil {
						if e = i.Commit(Selection{Active: old.Previous}); e != nil {
							return e
						}
						target = old.Previous
						current = target
						notice("运行版本异常退出，已回退至之前版本。")
						continue
					}
				}
			}
			return nil
		}
		target = i.Take(token)
		if target == "" {
			target = current
			notice("没有有效的更新请求，仍使用原版。")
			continue
		}
		trial = true
	}
}

func (i Install) CurrentStagedSlot(executable string) (string, error) {
	directory := filepath.Clean(filepath.Dir(executable))
	name := filepath.Base(filepath.Dir(directory))
	slot := strings.TrimPrefix(name, "slot-")
	if name == slot || !ValidDigest(slot) {
		return "", errors.New("当前程序不在签名更新槽位中")
	}
	valid, _, err := i.Slot(slot)
	if err != nil || !samePath(valid, directory) {
		return "", errors.New("当前更新槽位未通过验证")
	}
	return slot, nil
}

func (i Install) WaitActive(slot string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if i.Selection().Active == slot {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("更新监督进程未确认新版本")
}

func (i Install) PruneStaged(keep string) error {
	entries, err := os.ReadDir(i.Cache)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if active := i.Selection().Active; active != "" {
		keep = active
	}
	var first error
	for _, entry := range entries {
		name := entry.Name()
		digest := strings.TrimPrefix(name, "slot-")
		if name == digest || !ValidDigest(digest) || digest == keep {
			continue
		}
		path := filepath.Join(i.Cache, name)
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			if first == nil {
				first = errors.New("更新缓存包含无效槽位")
			}
			continue
		}
		if err = NoLinks(path); err == nil {
			err = os.RemoveAll(path)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

type childProcess struct {
	cmd  *exec.Cmd
	done chan int
}

// Done 返回子进程退出状态的通知通道。
func (p *childProcess) Done() <-chan int { return p.done }

// Stop 终止更新监督流程持有的子进程。
func (p *childProcess) Stop() { _ = p.cmd.Process.Kill() }

// Start 构造用于启动客户端子进程的函数。
func (i Install) Start(args []string) StartChild {
	return func(dir, token string, trial bool) (Child, error) {
		cmd := exec.Command(filepath.Join(dir, i.Edition+".exe"), args...)
		cmd.Dir = dir
		for _, v := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(v), "GUO_UPDATE_") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		cmd.Env = append(cmd.Env, "GUO_UPDATE_ROOT="+i.Root, "GUO_UPDATE_SESSION="+token, "GUO_UPDATE_PARENT_PID="+strconv.Itoa(os.Getpid()))
		if trial {
			cmd.Env = append(cmd.Env, "GUO_UPDATE_TRIAL=1")
		}
		if e := cmd.Start(); e != nil {
			return nil, e
		}
		p := &childProcess{cmd, make(chan int, 1)}
		go func() { _ = cmd.Wait(); p.done <- cmd.ProcessState.ExitCode() }()
		return p, nil
	}
}
