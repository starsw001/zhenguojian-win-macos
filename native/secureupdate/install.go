package secureupdate

import (
	"bytes"
	"duanjuapp/native/licensing"
	"duanjuapp/native/portable"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type inheritedLicenseState struct {
	path string
	body []byte
}

func captureLicenseState(root, edition, distributionID string) (inheritedLicenseState, error) {
	path := filepath.Join(root, "app-data", "licenses", edition, distributionID, "license-state.dat")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return inheritedLicenseState{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 512<<10 {
		return inheritedLicenseState{}, errors.New("原安装目录的账号许可证状态无效")
	}
	if err = portable.NoLinks(path); err != nil {
		return inheritedLicenseState{}, errors.New("原安装目录的账号许可证路径无效")
	}
	body, err := os.ReadFile(path)
	if err != nil || int64(len(body)) != info.Size() {
		return inheritedLicenseState{}, errors.New("无法读取原安装目录的账号许可证状态")
	}
	return inheritedLicenseState{path: path, body: body}, nil
}

func restoreLicenseState(state inheritedLicenseState) error {
	if state.path == "" {
		return nil
	}
	info, err := os.Lstat(state.path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("账号许可证状态被替换为无效文件")
		}
		current, readErr := os.ReadFile(state.path)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(current, state.body) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = portable.EnsureDirectory(filepath.Dir(state.path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(state.path), ".license-inherit-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		err = licensing.RestrictPrivateFile(name)
	}
	if err == nil {
		_, err = file.Write(state.body)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(name, state.path)
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func (i Install) manifest(dir, version string) (Manifest, error) {
	body, err := small(filepath.Join(dir, "distribution.json"), 512<<10)
	if err != nil {
		return Manifest{}, err
	}
	return getManifest(body, i.Key, i.Edition, i.ID, version)
}

func runtimeNames(manifest Manifest) []string {
	names := make([]string, 0, len(manifest.Files)+1)
	for name := range manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return append(names, "distribution.json")
}

func unionNames(a, b Manifest) []string {
	seen := map[string]bool{}
	for name := range a.Files {
		seen[name] = true
	}
	for name := range b.Files {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return append(names, "distribution.json")
}

func copyAtomic(source, target string, preferLink bool) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("更新源文件无效")
	}
	parent := filepath.Dir(target)
	if err = portable.EnsureDirectory(parent); err != nil {
		return err
	}
	if err = portable.NoLinks(parent); err != nil {
		return err
	}
	if targetInfo, targetErr := os.Lstat(target); targetErr == nil {
		if !targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("更新目标文件无效")
		}
	} else if !os.IsNotExist(targetErr) {
		return targetErr
	}
	file, err := os.CreateTemp(parent, ".replace-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err = file.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	if err = os.Remove(temporary); err != nil {
		return err
	}
	if preferLink {
		if err = os.Link(source, temporary); err == nil {
			if err = replaceFile(temporary, target); err == nil {
				return nil
			}
			os.Remove(temporary)
			return err
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(temporary)
		return closeErr
	}
	if err = replaceFile(temporary, target); err != nil {
		os.Remove(temporary)
	}
	return err
}

func mergeMutableDirectory(sourceRoot, targetRoot, name string) error {
	source := filepath.Join(sourceRoot, name)
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("旧版本用户目录无效")
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("旧版本用户目录包含链接")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if strings.EqualFold(name, "app-data") && strings.EqualFold(parts[0], "updates") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(targetRoot, name, relative)
		if entry.IsDir() {
			return portable.EnsureDirectory(target)
		}
		if !entry.Type().IsRegular() {
			return errors.New("旧版本用户目录包含非常规文件")
		}
		return copyAtomic(path, target, true)
	})
}

func (i Install) mergePreviousMutable(previous string) error {
	if previous == "" {
		return nil
	}
	dir, _, err := i.Slot(previous)
	if err != nil {
		return fmt.Errorf("读取更新前的用户数据：%w", err)
	}
	for _, name := range []string{"app-data", "download", "logs"} {
		if err = mergeMutableDirectory(dir, i.Root, name); err != nil {
			return fmt.Errorf("归并旧版本 %s：%w", name, err)
		}
	}
	return nil
}

func (i Install) restoreRuntime(backup string, existed map[string]bool, names []string, old Manifest) error {
	var first error
	for _, name := range names {
		target := filepath.Join(i.Root, filepath.FromSlash(name))
		if existed[name] {
			if err := copyAtomic(filepath.Join(backup, filepath.FromSlash(name)), target, true); err != nil && first == nil {
				first = err
			}
		} else if err := os.Remove(target); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}
	if first == nil {
		first = VerifyTree(i.Root, i.Key, i.Edition, i.ID, old.Version, false)
	}
	if err := i.Commit(Selection{}); first == nil {
		first = err
	}
	return first
}

func (i Install) ApplyStaged(slot string, validate func(string) error) (string, error) {
	if !ValidDigest(slot) {
		return "", errors.New("待安装版本无效")
	}
	selection := i.Selection()
	if selection.Active != slot {
		return "", errors.New("待安装版本尚未完成试启动确认")
	}
	source, version, err := i.Slot(slot)
	if err != nil || samePath(source, i.Root) {
		return "", errors.New("待安装版本目录无效")
	}
	if err = VerifyTree(i.Root, i.Key, i.Edition, i.ID, i.Base, false); err != nil {
		return "", fmt.Errorf("原安装目录未通过校验：%w", err)
	}
	oldManifest, err := i.manifest(i.Root, i.Base)
	if err != nil {
		return "", err
	}
	newManifest, err := i.manifest(source, version)
	if err != nil {
		return "", err
	}
	licenseState, err := captureLicenseState(i.Root, i.Edition, i.ID)
	if err != nil {
		return "", err
	}
	if err = i.mergePreviousMutable(selection.Previous); err != nil {
		return "", err
	}
	if err = restoreLicenseState(licenseState); err != nil {
		return "", fmt.Errorf("继承账号和许可证：%w", err)
	}
	backup, err := os.MkdirTemp(i.Cache, ".install-")
	if err != nil {
		return "", err
	}
	removeBackup := false
	defer func() {
		if removeBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	names := unionNames(oldManifest, newManifest)
	existed := make(map[string]bool, len(names))
	for _, name := range names {
		target := filepath.Join(i.Root, filepath.FromSlash(name))
		info, statErr := os.Lstat(target)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("原安装目录包含无效运行文件")
		}
		existed[name] = true
		if err = copyAtomic(target, filepath.Join(backup, filepath.FromSlash(name)), true); err != nil {
			return "", err
		}
	}
	fail := func(reason error) (string, error) {
		restoreErr := i.restoreRuntime(backup, existed, names, oldManifest)
		if restoreErr == nil {
			removeBackup = true
			return "", fmt.Errorf("原位更新未完成，已恢复旧版：%w", reason)
		}
		return "", fmt.Errorf("原位更新失败且自动恢复未完成（备份保留在 %s）：%v；恢复错误：%w", backup, reason, restoreErr)
	}
	for _, name := range runtimeNames(newManifest) {
		if err = copyAtomic(filepath.Join(source, filepath.FromSlash(name)), filepath.Join(i.Root, filepath.FromSlash(name)), false); err != nil {
			return fail(err)
		}
	}
	for name := range oldManifest.Files {
		if newManifest.Files[name] != "" {
			continue
		}
		if err = os.Remove(filepath.Join(i.Root, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
	}
	if err = VerifyTree(i.Root, i.Key, i.Edition, i.ID, version, false); err != nil {
		return fail(err)
	}
	if validate != nil {
		if err = validate(i.Root); err != nil {
			return fail(err)
		}
	}
	if err = restoreLicenseState(licenseState); err != nil {
		return fail(fmt.Errorf("确认账号和许可证继承：%w", err))
	}
	if err = i.Commit(Selection{}); err != nil {
		return fail(err)
	}
	removeBackup = true
	return version, nil
}
