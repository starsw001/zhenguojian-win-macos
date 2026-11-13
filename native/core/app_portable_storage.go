package core

import (
	"duanjuapp/native/portable"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// nativePortableRoot 识别当前版本目录中的独立应用数据目录。
func nativePortableRoot(directory string) string {
	if filepath.IsAbs(directory) && strings.EqualFold(filepath.Base(filepath.Clean(directory)), "app-data") {
		return filepath.Dir(filepath.Clean(directory))
	}
	return ""
}

// nativePortableSelectedPath 将便携目录内的相对路径解析为当前运行位置。
func nativePortableSelectedPath(directory, value, subtree string) (string, error) {
	root := nativePortableRoot(directory)
	if root == "" || value == "" {
		return value, nil
	}
	if !filepath.IsAbs(value) {
		if filepath.VolumeName(value) != "" {
			return "", errors.New("便携数据路径无效")
		}
		value = filepath.Join(root, filepath.FromSlash(value))
	}
	value = filepath.Clean(value)
	base := filepath.Join(root, subtree)
	rel, err := filepath.Rel(base, value)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errors.New("便携版数据必须保存在当前软件目录的 " + subtree + " 文件夹内")
	}
	return value, portable.NoLinks(value)
}

// nativePortableDownloadLocation 读取本版本下载位置，拒绝旧电脑的绝对外部路径。
func nativePortableDownloadLocation(directory string) (string, error) {
	root := nativePortableRoot(directory)
	path := filepath.Join(root, "download")
	file := filepath.Join(directory, "download-location.json")
	if err := portable.NoLinks(file); err != nil {
		return path, err
	}
	body, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return path, portable.NoLinks(path)
	}
	var saved struct {
		Directory string `json:"directory"`
	}
	if err != nil || len(body) > 16384 || json.Unmarshal(body, &saved) != nil || saved.Directory == "" {
		return path, errors.New("当前版本的下载位置记录无效，原文件已保留")
	}
	return nativePortableSelectedPath(directory, saved.Directory, "download")
}

// nativeDownloadLocationData 为便携版保存相对下载位置，其他平台沿用现有规则。
func nativeDownloadLocationData(directory, target, mode string) []byte {
	if root := nativePortableRoot(directory); root != "" {
		target, _ = filepath.Rel(root, target)
		target, mode = filepath.ToSlash(target), "portable"
	}
	body, _ := json.Marshal(map[string]string{"directory": target, "mode": mode})
	return body
}
