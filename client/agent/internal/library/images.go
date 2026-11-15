// images.go 按服务器/目标/版本解析与缓存图片URL；可见卡片按需调用，不代理图片字节。
// 最大并发4、相同请求合并、临时失败短退避；切服不接纳旧结果。见 docs/client-launch-media.md。
package library

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"time"

	"bili-history-tracker/client/agent/internal/remote"
)

const maxImageEntries = 2000

type ImageRecord struct {
	URL       string    `json:"url"`
	Seq       int64     `json:"seq"`
	CheckedAt time.Time `json:"checked_at"`
}

type ImageResult struct {
	URL       string `json:"url"`
	Seq       int64  `json:"seq"`
	Available bool   `json:"available"`
	Cached    bool   `json:"cached"`
}

type imageCall struct {
	done   chan struct{}
	result ImageResult
	err    error
}

type imageFailure struct {
	err   error
	until time.Time
}

func imageKey(kind, id string, seq int64) string {
	return kind + ":" + id + ":" + strconv.FormatInt(seq, 10)
}

// Image 从服务器既有详情快照获取地址，调用方不能指定任意URL。
// seq=0解析当前版本并最多缓存15分钟；明确版本按版本缓存直至容量淘汰。
func (s *Service) Image(ctx context.Context, kind, input string, seq int64) (ImageResult, error) {
	id, err := Normalize(kind, input)
	if err != nil || seq < 0 {
		return ImageResult{}, &Error{Message: "图片目标或版本无效。", Status: 400}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	key := imageKey(kind, id, seq)
	s.mu.Lock()
	base := s.state.ServerURL
	if record, ok := s.state.Images[key]; ok && (seq == 0 || record.Seq == seq) && (seq != 0 || time.Since(record.CheckedAt) < 15*time.Minute) && (record.URL == "" || remote.NormalizeImageURL(record.URL) == record.URL) {
		s.mu.Unlock()
		return ImageResult{URL: record.URL, Seq: record.Seq, Available: record.URL != "", Cached: true}, nil
	}
	flightKey := base + "|" + key
	if failure, ok := s.imageErrors[flightKey]; ok && time.Now().Before(failure.until) {
		s.mu.Unlock()
		return ImageResult{}, failure.err
	}
	if call := s.imageCalls[flightKey]; call != nil {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.result, call.err
		case <-ctx.Done():
			return ImageResult{}, ctx.Err()
		}
	}
	call := &imageCall{done: make(chan struct{})}
	s.imageCalls[flightKey] = call
	s.mu.Unlock()
	result, err := s.readImage(ctx, base, kind, id, seq)
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL {
		err = &Error{Message: "服务器已切换，请重新加载图片。", Status: 409}
	}
	if err == nil {
		s.rememberImageLocked(key, ImageRecord{URL: result.URL, Seq: result.Seq, CheckedAt: time.Now().UTC()})
	} else if ctx.Err() == nil && base == s.state.ServerURL {
		if len(s.imageErrors) >= maxImageEntries {
			clear(s.imageErrors)
		}
		s.imageErrors[flightKey] = imageFailure{err: err, until: time.Now().Add(time.Minute)}
	}
	call.result, call.err = result, err
	delete(s.imageCalls, flightKey)
	close(call.done)
	return result, err
}

func (s *Service) readImage(ctx context.Context, base, kind, id string, seq int64) (ImageResult, error) {
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-ctx.Done():
		return ImageResult{}, ctx.Err()
	}
	data, err := s.client.Detail(ctx, base, kind, id, 1, seq)
	if err != nil {
		return ImageResult{}, err
	}
	if seq == 0 && data.Item.Seq > 0 {
		seq = data.Item.Seq
		data, err = s.client.Detail(ctx, base, kind, id, 1, seq)
		if err != nil {
			return ImageResult{}, err
		}
	}
	if seq > 0 && (data.SnapshotSeq != seq || len(data.Snapshot) == 0) {
		return ImageResult{}, &Error{Message: "服务器未返回所选图片快照。", Status: 502}
	}
	address := remote.SnapshotImageURL(kind, data.Snapshot)
	return ImageResult{URL: address, Seq: seq, Available: address != ""}, nil
}

// rememberImageLocked 仅改变已知图片索引；缓存落盘失败不阻止本次URL展示，也不修改原内存状态。
func (s *Service) rememberImageLocked(key string, record ImageRecord) {
	if old, ok := s.state.Images[key]; ok && old.URL == record.URL && old.Seq == record.Seq && time.Since(old.CheckedAt) < 15*time.Minute {
		return
	}
	next := s.copyState()
	next.Images = maps.Clone(s.state.Images)
	if next.Images == nil {
		next.Images = map[string]ImageRecord{}
	}
	next.Images[key] = record
	for len(next.Images) > maxImageEntries {
		oldest := ""
		for entry, value := range next.Images {
			if oldest == "" || value.CheckedAt.Before(next.Images[oldest].CheckedAt) {
				oldest = entry
			}
		}
		delete(next.Images, oldest)
	}
	_ = s.save(next)
}

func (s *Service) rememberSnapshotLocked(kind, id string, seq int64, raw json.RawMessage) {
	if seq > 0 && len(raw) > 0 {
		s.rememberImageLocked(imageKey(kind, id, seq), ImageRecord{URL: remote.SnapshotImageURL(kind, raw), Seq: seq, CheckedAt: time.Now().UTC()})
	}
}
