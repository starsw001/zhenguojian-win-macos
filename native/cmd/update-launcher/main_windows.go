package main

import (
	"context"
	"duanjuapp/native/licensing"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"duanjuapp/native/secureupdate"
	"golang.org/x/sys/windows"
)

var edition, version, publicKey, distributionID string

// tell 通过 Windows 消息框显示启动器提示。
func tell(s string) {
	p, _ := windows.UTF16PtrFromString(s)
	title, _ := windows.UTF16PtrFromString("软件更新")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(title)), 0x40)
}

func cleanUpdateEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GUO_UPDATE_") {
			result = append(result, value)
		}
	}
	return result
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	if pid <= 0 || pid == os.Getpid() {
		return errors.New("更新等待进程无效")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
	if err != nil {
		return err
	}
	if result != windows.WAIT_OBJECT_0 {
		return errors.New("等待旧版本退出超时")
	}
	return nil
}

func verifyInstalledRoot(root string) error {
	command := exec.Command(filepath.Join(root, edition+".exe"), "--verify-window-host")
	command.Dir = root
	command.Env = cleanUpdateEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("原目录新版启动校验失败：%s", strings.TrimSpace(string(output)))
	}
	return nil
}

func startInstalledRoot(root string) error {
	command := exec.Command(filepath.Join(root, edition+".exe"))
	command.Dir = root
	command.Env = cleanUpdateEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func runSilentStagedInstall(root string) (int, error) {
	i, err := secureupdate.Runtime(publicKey, edition)
	if err != nil {
		return 1, err
	}
	if i.ID != distributionID || os.Getenv("GUO_UPDATE_PARENT_PID") != strconv.Itoa(os.Getppid()) {
		return 1, errors.New("静默更新启动上下文不符")
	}
	executable, err := os.Executable()
	if err != nil {
		return 1, err
	}
	slot, err := i.CurrentStagedSlot(executable)
	if err != nil || !strings.EqualFold(filepath.Dir(executable), root) {
		return 1, errors.New("静默更新程序目录无效")
	}
	session := os.Getenv("GUO_UPDATE_SESSION")
	if err = i.Ack(session); err != nil {
		return 1, err
	}
	if err = i.WaitActive(slot, 15*time.Second); err != nil {
		return 1, err
	}
	if err = i.StartInstaller(slot); err != nil {
		_ = i.Commit(secureupdate.Selection{})
		return 1, err
	}
	return 0, nil
}

func applyStagedUpdate(slot string) error {
	if os.Getenv("GUO_UPDATE_TRIAL") != "1" || !secureupdate.ValidDigest(slot) {
		return errors.New("原位更新上下文无效")
	}
	clientPID, err := strconv.Atoi(os.Getenv("GUO_UPDATE_INSTALL_PARENT_PID"))
	if err != nil || clientPID != os.Getppid() {
		return errors.New("原位更新助手的父进程不匹配")
	}
	supervisorPID, err := strconv.Atoi(os.Getenv("GUO_UPDATE_PARENT_PID"))
	if err != nil || supervisorPID <= 0 || supervisorPID == clientPID {
		return errors.New("更新监督进程无效")
	}
	install, err := secureupdate.Runtime(publicKey, edition)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	current, err := install.CurrentStagedSlot(executable)
	if err != nil || current != slot || install.Selection().Active != slot {
		return errors.New("试启动确认与待覆盖版本不一致")
	}
	if err = waitForProcessExit(clientPID, 60*time.Second); err != nil {
		return err
	}
	if err = waitForProcessExit(supervisorPID, 60*time.Second); err != nil {
		return err
	}
	_, err = install.ApplyStaged(slot, verifyInstalledRoot)
	if err != nil {
		_ = install.Commit(secureupdate.Selection{})
		if restartErr := startInstalledRoot(install.Root); restartErr != nil {
			return fmt.Errorf("%v；旧版重新启动失败：%w", err, restartErr)
		}
		return err
	}
	return startInstalledRoot(install.Root)
}

// runWindow 从指定版本目录启动客户端窗口，并返回其退出状态。
func runWindow(dir string, check bool) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	i, err := secureupdate.Runtime(publicKey, edition)
	if err != nil {
		return 1, err
	}
	if i.ID != distributionID || os.Getenv("GUO_UPDATE_PARENT_PID") != strconv.Itoa(os.Getppid()) {
		return 1, errors.New("软件启动上下文不符")
	}
	slot := ""
	if !strings.EqualFold(dir, i.Root) {
		name := filepath.Base(filepath.Dir(dir))
		slot = strings.TrimPrefix(name, "slot-")
		if name == slot || !secureupdate.ValidDigest(slot) {
			return 1, errors.New("无效的签名版本目录")
		}
	}
	validDir, validVersion, err := i.Slot(slot)
	if err != nil {
		return 1, err
	}
	if validVersion != version || !strings.EqualFold(dir, validDir) {
		return 1, errors.New("签名版本与当前程序不符")
	}
	host, err := windows.LoadLibraryEx(filepath.Join(dir, "guo_window.dll"), 0,
		windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_APPLICATION_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return 1, fmt.Errorf("无法加载软件界面组件：%w", err)
	}
	validate, err := windows.GetProcAddress(host, "GuoCheckWindowHost")
	if err != nil {
		return 1, err
	}
	open, err := windows.GetProcAddress(host, "GuoRunWindow")
	if err != nil {
		return 1, err
	}
	name, _ := windows.BytePtrFromString(edition + ".exe")
	build, _ := windows.BytePtrFromString(version)
	ok, _, _ := syscall.SyscallN(validate, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(build)))
	runtime.KeepAlive(name)
	runtime.KeepAlive(build)
	if ok != 1 {
		return 1, errors.New("软件界面与当前产品或版本不符")
	}
	if check {
		return 0, nil
	}
	result, _, _ := syscall.SyscallN(open)
	return int(int32(result)), nil
}

// launch 核对当前安装与更新状态，选择需要启动的软件版本。
func launch(check bool) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	if !strings.EqualFold(filepath.Base(exe), edition+".exe") {
		return 1, errors.New("软件主程序名称不符")
	}
	root := filepath.Dir(exe)
	if os.Getenv("GUO_UPDATE_PARENT_PID") == strconv.Itoa(os.Getppid()) &&
		secureupdate.ValidToken(os.Getenv("GUO_UPDATE_SESSION")) {
		if !check && os.Getenv("GUO_UPDATE_TRIAL") == "1" {
			return runSilentStagedInstall(root)
		}
		return runWindow(root, check)
	}
	i, err := secureupdate.Locate(root, edition, distributionID, publicKey, version)
	if err != nil {
		return 1, err
	}
	if check {
		dir, _, err := i.Slot("")
		if err != nil {
			return 1, err
		}
		child, err := i.Start(os.Args[1:])(dir, secureupdate.Random(), false)
		if err != nil {
			return 1, err
		}
		return <-child.Done(), nil
	}
	go func() {
		for attempt := 0; attempt < 30; attempt++ {
			if i.Selection().Active != "" || i.PruneStaged("") == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	h, alreadyRunning, err := acquireProductLaunchMutex(productInstanceKey())
	if h != 0 {
		defer windows.CloseHandle(h)
	}
	if alreadyRunning {
		processes, _ := productProcesses(root, true)
		tell(runningProductMessage(newestProcessVersion(processes)))
		return 0, nil
	}
	if err != nil {
		return 1, errors.New("无法取得启动锁")
	}
	if processes, processErr := productProcesses(root, false); processErr == nil && len(processes) > 0 {
		tell(runningProductMessage(newestProcessVersion(processes)))
		return 0, nil
	}
	guardStop := make(chan struct{})
	go guardLegacyInstances(root, guardStop, tell)
	defer close(guardStop)
	if err = i.Supervise(i.Start(os.Args[1:]), 120*time.Second, tell); err != nil {
		return 1, err
	}
	return 0, nil
}

// checkReleaseChannel 检查本软件配置的更新发布渠道。
func checkReleaseChannel() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(exe), edition+".exe") {
		return errors.New("当前程序名称不符")
	}
	if err = secureupdate.VerifyTree(filepath.Dir(exe), publicKey, edition, distributionID, version, false); err != nil {
		return err
	}
	tr := licensing.SystemTransport()
	tr.ResponseHeaderTimeout = 15 * time.Second
	client := secureupdate.NewPublicClient(tr)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	r, o, err := secureupdate.CheckRelease(ctx, client, edition, distributionID, publicKey, version)
	if errors.Is(err, secureupdate.ErrNoPublishedRelease) {
		fmt.Printf("UPDATE_CHANNEL edition=%s current=%s noPublicRelease=true\n", edition, version)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("UPDATE_CHANNEL edition=%s current=%s publishedTag=%s newerSignedOffer=%t\n", edition, version, r.Tag, o != nil)
	return nil
}

// main 启动 Windows 更新启动器，并处理启动失败提示。
func main() {
	if len(os.Args) == 3 && os.Args[1] == "--apply-staged-update" {
		if err := applyStagedUpdate(os.Args[2]); err != nil {
			tell("更新未能覆盖到原安装目录，旧版文件和用户下载已保留：" + err.Error())
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--check-release-channel" {
		if err := checkReleaseChannel(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	check := len(os.Args) == 2 && os.Args[1] == "--verify-window-host"
	code, err := launch(check)
	if err != nil {
		if check {
			fmt.Fprintln(os.Stderr, err)
		} else {
			tell("无法安全启动软件：" + err.Error())
		}
	} else if check && code == 0 && os.Getenv("GUO_UPDATE_PARENT_PID") != strconv.Itoa(os.Getppid()) {
		fmt.Printf("HOST_OK edition=%s version=%s\n", edition, version)
	}
	os.Exit(code)
}
