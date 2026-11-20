// directory.go 以相同存储锁切换本地目录，复制已知状态并保留原文件。
// 初始目录增加位置指向并保留原文件；失败不切换，不覆盖非空目标。见 docs/client-workspace.md。
package localstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type DirectoryInfo struct {
	Directory       string `json:"data_directory"`
	LaunchDirectory string `json:"launch_directory"`
	Bytes           int64  `json:"state_bytes"`
}

var stateFiles = []string{"library.json", "scan.json", "cookie.protected"}

func (s *Store) DirectoryInfo() DirectoryInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := DirectoryInfo{Directory: s.directory, LaunchDirectory: s.anchor}
	for _, name := range stateFiles {
		if f, err := os.Stat(filepath.Join(s.directory, name)); err == nil {
			info.Bytes += f.Size()
		}
	}
	return info
}

// SwitchDirectory 与状态读写互斥，成功时复制一份一致的已落盘状态，再原子更新启动指向。
// 只接收本机绝对路径，不迁移任意附加文件或删除原目录；新的目标必须为空。
func (s *Store) SwitchDirectory(input string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	input = strings.TrimSpace(input)
	if !filepath.IsAbs(input) {
		return errors.New("工作目录必须是本机绝对路径")
	}
	target := filepath.Clean(input)
	if existing, err := filepath.EvalSymlinks(target); err == nil {
		target = existing
	}
	if target == s.directory {
		return nil
	}
	rel, err := filepath.Rel(s.directory, target)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("新目录不能位于当前工作目录内部")
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return errors.New("无法创建目标目录，请检查路径和权限")
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return errors.New("无法解析目标目录")
	}
	rel, err = filepath.Rel(s.directory, target)
	if target == s.directory || (err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return errors.New("新目录不能位于当前工作目录内部")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		return errors.New("目标目录必须为空，现有数据未更改")
	}
	for _, name := range stateFiles {
		file := filepath.Join(s.directory, name)
		info, e := os.Lstat(file)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("原目录状态文件不可读取，未切换目录")
		}
		data, e := readLimited(file, 64<<20)
		if e != nil {
			return errors.New("复制状态失败，原目录仍有效")
		}
		if e = writeAt(target, name, data); e != nil {
			return errors.New("目标写入失败，原目录仍有效；目标可能保留部分副本")
		}
	}
	pointer, _ := json.Marshal(map[string]string{"data_directory": target})
	if err = writeAt(s.anchor, "location.json", pointer); err != nil {
		return errors.New("无法保存目录位置，原目录仍有效")
	}
	s.directory = target
	return nil
}
