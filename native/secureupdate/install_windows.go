package secureupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func (i Install) StartInstaller(slot string) error {
	if os.Getenv("GUO_UPDATE_TRIAL") != "1" || !ValidDigest(slot) {
		return errors.New("原位更新启动上下文无效")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	current, err := i.CurrentStagedSlot(executable)
	if err != nil || current != slot {
		return errors.New("原位更新程序与已确认版本不匹配")
	}
	directory, _, err := i.Slot(slot)
	if err != nil || !samePath(directory, filepath.Dir(executable)) {
		return errors.New("原位更新程序目录无效")
	}
	command := exec.Command(executable, "--apply-staged-update", slot)
	command.Dir = directory
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GUO_UPDATE_INSTALL_PARENT_PID=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GUO_UPDATE_INSTALL_PARENT_PID="+strconv.Itoa(os.Getpid()))
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	if err = command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
