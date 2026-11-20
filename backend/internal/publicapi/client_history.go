// client_history.go 提供有界历史批读、历史版本和本地资料失败时的轻量兜底。
// 读取不调用平台、不变更跟踪；见 docs/local-first-metadata.md。
package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

func metadataItem(i storage.ContentItem) map[string]any {
	return map[string]any{"kind": i.Kind, "id": i.ID, "name": i.Name, "cover": i.Cover,
		"owner_id": i.OwnerID, "owner_name": i.OwnerName, "metric": i.Metric, "published_at": i.PublishedAt,
		"seq": i.Seq, "observed_at": i.ObservedAt, "recorded": hasRecordedContent(i)}
}

func (s *Server) batchMetrics(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) {
		return
	}
	var input struct {
		Kind        string   `json:"kind"`
		IDs         []string `json:"ids"`
		Days        int      `json:"days"`
		FallbackIDs []string `json:"fallback_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Days == 0 {
		input.Days = 7
	}
	seen := map[string]bool{}
	valid := len(input.IDs) > 0 && len(input.IDs) <= 24 && input.Days == 7
	for _, id := range input.IDs {
		valid = valid && validID(input.Kind, id) && !seen[id]
		seen[id] = true
	}
	fallback := map[string]bool{}
	for _, id := range input.FallbackIDs {
		valid = valid && seen[id] && !fallback[id]
		fallback[id] = true
	}
	if !valid || len(input.FallbackIDs) > 24 {
		writeJSON(w, 400, map[string]string{"error": "批量参数无效；每轮最多24个不同目标，只支持7日变化。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	series, err := s.reader.MetricsBatch(ctx, s.platform, input.Kind, input.IDs, 7, time.Now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取批量指标。"})
		return
	}
	var details map[string]storage.ContentItem
	if len(input.FallbackIDs) > 0 {
		details, err = s.reader.MetadataBatch(ctx, s.platform, input.Kind, input.FallbackIDs)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取兜底资料。"})
			return
		}
	}
	items := make([]map[string]any, 0, len(input.IDs))
	for _, id := range input.IDs {
		i := map[string]any{"kind": input.Kind, "id": id, "points": series[id].Points, "truncated": series[id].Truncated}
		if detail, exists := details[id]; exists {
			i["fallback"] = metadataItem(detail)
		}
		items = append(items, i)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	if !validID(kind, id) {
		writeJSON(w, 400, map[string]string{"error": "目标标识无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	items, err := s.reader.MetadataBatch(ctx, s.platform, kind, []string{id})
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取兜底资料。"})
		return
	}
	item, exists := items[id]
	if !exists {
		business(w, "DETAIL_NOT_RECORDED", "服务器暂无该目标的资料。", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"item": metadataItem(item)})
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	days, page, seq := 30, 1, int64(0)
	if raw := r.URL.Query().Get("days"); raw != "" {
		days = rangeDays(raw, -1)
	}
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
	}
	if raw := r.URL.Query().Get("seq"); raw != "" && err == nil {
		seq, err = strconv.ParseInt(raw, 10, 64)
		if seq < 1 {
			err = errors.New("invalid seq")
		}
	}
	if !validID(kind, id) || days < 0 || page < 1 || page > 100000 || err != nil {
		writeJSON(w, 400, map[string]string{"error": "历史查询参数无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if seq > 0 {
		snapshot, e := s.reader.ReadSnapshot(ctx, s.platform, kind, id, seq)
		if errors.Is(e, sql.ErrNoRows) {
			business(w, "SNAPSHOT_NOT_FOUND", "没有该版本快照。", nil)
			return
		}
		if e != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取历史快照。"})
			return
		}
		writeJSON(w, 200, map[string]any{"item": map[string]any{"kind": kind, "id": id, "seq": seq, "recorded": true, "observed_at": snapshot.At}, "snapshot": json.RawMessage(snapshot.Payload), "snapshot_seq": seq})
		return
	}
	head, e := s.reader.HistoryHead(ctx, s.platform, kind, id)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !hasRecordedContent(head) {
		business(w, "DETAIL_NOT_RECORDED", "服务器暂无该目标的成功观测资料。", nil)
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取历史指针。"})
		return
	}
	versions, e := s.reader.Versions(ctx, s.platform, kind, id, "", 0, page, 10)
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取版本。"})
		return
	}
	series, e := s.reader.Metrics(ctx, s.platform, kind, id, days, time.Now())
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取指标。"})
		return
	}
	entries := make([]map[string]any, 0, len(versions.Items))
	for _, v := range versions.Items {
		entries = append(entries, map[string]any{"seq": v.Seq, "at": v.At, "name": v.Name})
	}
	writeJSON(w, 200, map[string]any{"item": map[string]any{"kind": kind, "id": id, "seq": head.Seq, "recorded": true, "observed_at": head.ObservedAt}, "metrics": series, "versions": entries, "version_page": versions.Page, "version_total": versions.Total, "version_limit": versions.Limit})
}
