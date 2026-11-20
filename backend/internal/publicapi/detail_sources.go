// detail_sources.go 提供独立的历史、保存资料和按需版本接口。
// 不触发采集；正常历史两次 SQL，保存资料一次。见 docs/client-detail-data-sources.md。
package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) detailSources(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	if !validID(kind, id) {
		writeJSON(w, 400, map[string]string{"error": "目标标识无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	switch r.PathValue("part") {
	case "saved":
		value, err := s.reader.ReadSavedContent(ctx, s.platform, kind, id)
		if errors.Is(err, sql.ErrNoRows) {
			business(w, "DETAIL_NOT_RECORDED", "服务器没有该目标的成功观测资料。", nil)
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取保存资料。"})
			return
		}
		item := clientItem(value.Item)
		item["cover"] = value.Item.Cover
		writeJSON(w, 200, map[string]any{"item": item, "description": value.Description, "snapshot_at": value.SnapshotAt})
	case "versions":
		before := int64(0)
		if raw := r.URL.Query().Get("before"); raw != "" {
			var err error
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 1 {
				writeJSON(w, 400, map[string]string{"error": "版本游标无效。"})
				return
			}
		}
		page, err := s.reader.ReadDetailVersions(ctx, s.platform, kind, id, before)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取版本列表。"})
			return
		}
		entries := make([]map[string]any, 0, len(page.Items))
		for _, v := range page.Items {
			entries = append(entries, map[string]any{"seq": v.Seq, "at": v.At})
		}
		writeJSON(w, 200, map[string]any{"kind": kind, "id": id, "items": entries, "has_more": page.HasMore, "next_before": page.NextBefore})
	case "snapshot":
		seq, err := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
		if err != nil || seq < 1 {
			writeJSON(w, 400, map[string]string{"error": "版本号无效。"})
			return
		}
		value, err := s.reader.ReadSnapshot(ctx, s.platform, kind, id, seq)
		if errors.Is(err, sql.ErrNoRows) {
			business(w, "SNAPSHOT_NOT_FOUND", "没有该版本快照。", nil)
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "无法读取快照。"})
			return
		}
		writeJSON(w, 200, map[string]any{"kind": kind, "id": id, "seq": value.Seq, "at": value.At, "snapshot": json.RawMessage(value.Payload)})
	default:
		writeJSON(w, 404, map[string]string{"error": "未知详情接口。"})
	}
}

func (s *Server) detailHistory(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		days = rangeDays(raw, -1)
	}
	if !validID(kind, id) || days < 0 {
		writeJSON(w, 400, map[string]string{"error": "目标或时间范围无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	record, err := s.reader.ReadDetailTarget(ctx, s.platform, kind, id, time.Now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取记录信息。"})
		return
	}
	if record.Tracking && s.tracker.IsRunning(kind, id) {
		record.Status = "running"
	}
	series, err := s.reader.Metrics(ctx, s.platform, kind, id, days, time.Now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取历史指标。"})
		return
	}
	target := targetState{Kind: kind, ID: id, Recorded: record.Recorded, Tracking: record.Tracking, TrackingState: record.TrackingState, Status: record.Status}
	writeJSON(w, 200, map[string]any{"record": record, "target": target, "metrics": series})
}
