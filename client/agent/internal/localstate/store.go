// Package localstate 负责客户端固定目录下的关注库、服务器配置、扫描状态与 Cookie 持久化。
// Windows Cookie 使用当前用户 DPAPI 保护；其他平台仅在进程内保留，绝不明文回退。
// 写入先同步临时文件再原子替换，不接受 HTTP 调用方指定文件名。见 docs/client-discovery.md、docs/client-library.md。
package localstate

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Store struct{ directory string }

func New(directory string) (*Store, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &Store{directory: directory}, nil
}

func (s *Store) PersistentCookie() bool { return persistentCookie }

func (s *Store) LoadCookie() (string, error) {
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
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.write("scan.json", data)
}

func (s *Store) LoadLibrary(target any) error {
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
	f, err := os.CreateTemp(s.directory, ".bht-*")
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
	return os.Rename(temporary, filepath.Join(s.directory, name))
}
