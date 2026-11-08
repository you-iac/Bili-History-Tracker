// log.go 实现查询日志：保留最近若干条供后台查看，可选追加到文件。

package tracker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry 是一次查询或发现动作的结果。
type Entry struct {
	Time         time.Time `json:"time"`
	Level        string    `json:"level"`
	Target       string    `json:"target"`
	Action       string    `json:"action"`
	Message      string    `json:"message"`
	Detail       string    `json:"detail,omitempty"`
	DurationMS   int64     `json:"duration_ms"`
	VersionAdded bool      `json:"version_added,omitempty"`
}

// Log 是并发安全的环形查询日志。
type Log struct {
	mu      sync.Mutex
	entries []Entry
	max     int
	file    *os.File
}

// NewLog 创建日志。path 为空时只保留内存记录。
func NewLog(path string, max int) (*Log, error) {
	if max <= 0 {
		max = 500
	}
	log := &Log{max: max}
	if path == "" {
		return log, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open query log: %w", err)
	}
	log.file = file
	// 有界恢复文件尾部的有效 JSON 行；不将全部历史日志加载到内存。
	if info, err := file.Stat(); err == nil {
		offset := info.Size() - (2 << 20)
		if offset < 0 {
			offset = 0
		}
		scanner := bufio.NewScanner(io.NewSectionReader(file, offset, info.Size()-offset))
		scanner.Buffer(make([]byte, 4096), 2<<20)
		if offset > 0 {
			scanner.Scan()
		}
		for scanner.Scan() {
			var entry Entry
			if json.Unmarshal(scanner.Bytes(), &entry) == nil && !entry.Time.IsZero() {
				log.entries = append(log.entries, entry)
				if len(log.entries) > max {
					log.entries = log.entries[1:]
				}
			}
		}
	}
	return log, nil
}

// Add 追加一条日志，并在有文件时同步写入一行 JSON。
func (l *Log) Add(entry Entry) {
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
	if len(l.entries) > l.max {
		l.entries = append([]Entry(nil), l.entries[len(l.entries)-l.max:]...)
	}
	if l.file != nil {
		if line, err := json.Marshal(entry); err == nil {
			_, _ = l.file.Write(append(line, '\n'))
		}
	}
}

// Recent 返回最近 n 条日志，按时间倒序排列。
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.entries) {
		n = len(l.entries)
	}
	result := make([]Entry, 0, n)
	for i := len(l.entries) - 1; i >= 0 && len(result) < n; i-- {
		result = append(result, l.entries[i])
	}
	return result
}

// Close 关闭日志文件。
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
