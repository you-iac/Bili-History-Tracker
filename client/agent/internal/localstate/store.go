// Package localstate 管理可切换工作目录中的关注库、服务器配置、扫描状态与 Cookie。
// Windows Cookie 使用当前用户 DPAPI 保护；其他平台仅在进程内保留，绝不明文回退。
// 状态写入与目录迁移共用存储锁，先同步临时文件再原子替换；只迁移已知状态文件。
// 初始目录保留启动指向，见 docs/client-workspace.md、docs/client-discovery.md。
package localstate

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu        sync.RWMutex
	directory string
	anchor    string
}

func New(directory string) (*Store, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	anchor := directory
	seen := map[string]bool{}
	for range 8 {
		canonical, e := filepath.EvalSymlinks(directory)
		if e != nil {
			return nil, e
		}
		if seen[canonical] {
			return nil, errors.New("数据目录指向形成循环")
		}
		seen[canonical] = true
		data, e := readLimited(filepath.Join(directory, "location.json"), 16<<10)
		if errors.Is(e, os.ErrNotExist) {
			return &Store{directory: canonical, anchor: anchor}, nil
		}
		if e != nil {
			return nil, e
		}
		var location struct {
			Directory string `json:"data_directory"`
		}
		if json.Unmarshal(data, &location) != nil || !filepath.IsAbs(location.Directory) {
			return nil, errors.New("数据目录指向无效")
		}
		info, e := os.Stat(location.Directory)
		if e != nil || !info.IsDir() {
			return nil, errors.New("已配置的数据目录不可用，请恢复该目录后启动")
		}
		directory = location.Directory
	}
	return nil, errors.New("数据目录指向层级过多")
}

func (s *Store) PersistentCookie() bool { return persistentCookie }

func (s *Store) LoadCookie() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !persistentCookie {
		return "", nil
	}
	data, err := readLimited(filepath.Join(s.directory, "cookie.protected"), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("读取受保护 Cookie 失败，请检查本地数据目录")
	}
	plain, err := unprotect(data)
	if err != nil {
		return "", errors.New("无法解密 Cookie，请使用原 Windows 用户启动，或移除 cookie.protected 后重新配置")
	}
	return string(plain), nil
}

func (s *Store) SaveCookie(cookie string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !persistentCookie {
		return nil
	}
	if cookie == "" {
		err := os.Remove(filepath.Join(s.directory, "cookie.protected"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := protect([]byte(cookie))
	if err != nil {
		return errors.New("系统无法保护 Cookie，未保存本次配置")
	}
	return s.write("cookie.protected", data)
}

func (s *Store) LoadScan(target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := readLimited(filepath.Join(s.directory, "scan.json"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *Store) SaveScan(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.write("scan.json", data)
}

func (s *Store) LoadHistory(target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := readLimited(filepath.Join(s.directory, "history.json"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *Store) SaveHistory(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.write("history.json", data)
}

func (s *Store) LoadLibrary(target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := readLimited(filepath.Join(s.directory, "library.json"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func (s *Store) SaveLibrary(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > 64<<20 {
		return errors.New("关注库超出大小限制")
	}
	return s.write("library.json", data)
}

func readLimited(filename string, limit int64) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("本地状态文件超出安全读取范围")
	}
	return data, err
}

func (s *Store) write(name string, data []byte) error {
	return writeAt(s.directory, name, data)
}

func writeAt(directory, name string, data []byte) error {
	f, err := os.CreateTemp(directory, ".bht-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(directory, name))
}
