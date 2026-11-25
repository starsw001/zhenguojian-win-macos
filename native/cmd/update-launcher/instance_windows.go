package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"duanjuapp/native/secureupdate"
	"golang.org/x/sys/windows"
)

type productProcess struct {
	pid, parent uint32
	path, root  string
	version     string
}

func productInstanceKey() string {
	return strings.ToLower(edition) + "|" + distributionID
}

func productDisplayName() string {
	if edition == "zhenguoshang" {
		return "真果赏"
	}
	return "红果赏"
}

func acquireProductLaunchMutex(key string) (windows.Handle, bool, error) {
	hash := sha256.Sum256([]byte(key))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\GuoApp-Product-%x", hash[:16]))
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		return handle, true, nil
	}
	return handle, false, err
}

func processExecutable(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil || size == 0 {
		return ""
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:size]))
}

func processProduct(path string) (string, bool) {
	body, err := os.ReadFile(filepath.Join(filepath.Dir(path), "distribution.json"))
	if err != nil || len(body) > 512<<10 {
		return "", false
	}
	var envelope secureupdate.Envelope
	if json.Unmarshal(body, &envelope) != nil {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return "", false
	}
	var manifest secureupdate.Manifest
	if json.Unmarshal(payload, &manifest) != nil || manifest.Edition != edition || manifest.DistributionID != distributionID || manifest.Version == "" {
		return "", false
	}
	return manifest.Version, true
}

func processInsideRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	relative = strings.ToLower(filepath.ToSlash(relative))
	return relative == strings.ToLower(edition+".exe") || strings.HasPrefix(relative, "app-data/updates/")
}

func productProcesses(root string, includeRoot bool) ([]productProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err = windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var result []productProcess
	for {
		if entry.ProcessID != uint32(os.Getpid()) && strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), edition+".exe") {
			path := processExecutable(entry.ProcessID)
			if path != "" && (includeRoot || !processInsideRoot(root, path)) {
				if runningVersion, valid := processProduct(path); valid {
					result = append(result, productProcess{entry.ProcessID, entry.ParentProcessID, path, filepath.Dir(path), runningVersion})
				}
			}
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		if err = windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, err
	}
	return result, nil
}

func newestProcessVersion(processes []productProcess) string {
	running := ""
	for _, process := range processes {
		if running == "" || secureupdate.Newer(process.version, running) {
			running = process.version
		}
	}
	return running
}

func runningProductMessage(running string) string {
	name := productDisplayName()
	if running == "" {
		return name + " 已在运行，请使用已经打开的窗口。"
	}
	if running == version {
		return fmt.Sprintf("%s %s 已在运行，请使用已经打开的窗口。", name, running)
	}
	if secureupdate.Newer(running, version) {
		return fmt.Sprintf("%s 较新版本 %s 已在运行，当前版本 %s 不会重复启动。", name, running, version)
	}
	return fmt.Sprintf("%s 旧版本 %s 已在运行，请先退出旧版后再启动 %s。", name, running, version)
}

func terminateProductProcesses(processes []productProcess) int {
	parents := make(map[uint32]bool, len(processes))
	for _, process := range processes {
		parents[process.pid] = true
	}
	sort.SliceStable(processes, func(a, b int) bool {
		aChild := parents[processes[a].parent]
		bChild := parents[processes[b].parent]
		if aChild != bChild {
			return !aChild
		}
		return processes[a].pid < processes[b].pid
	})
	terminated := 0
	for _, process := range processes {
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, process.pid)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(handle, 0) == nil {
			terminated++
		}
		windows.CloseHandle(handle)
	}
	return terminated
}

func guardLegacyInstances(root string, stop <-chan struct{}, notice func(string)) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	notified := map[string]time.Time{}
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			processes, err := productProcesses(root, false)
			if err != nil || len(processes) == 0 {
				continue
			}
			groups := map[string][]productProcess{}
			for _, process := range processes {
				groups[strings.ToLower(process.root)] = append(groups[strings.ToLower(process.root)], process)
			}
			for key, group := range groups {
				running := newestProcessVersion(group)
				if secureupdate.Newer(running, version) || terminateProductProcesses(group) == 0 {
					continue
				}
				if last := notified[key]; time.Since(last) < 10*time.Second {
					continue
				}
				notified[key] = time.Now()
				message := fmt.Sprintf("检测到%s旧版本 %s 尝试启动，已阻止重复运行。请使用正在运行的 %s。", productDisplayName(), running, version)
				if running == version {
					message = fmt.Sprintf("检测到%s %s 从另一目录重复启动，已阻止。请使用已经打开的窗口。", productDisplayName(), running)
				}
				go notice(message)
			}
		}
	}
}
