// client.go 将8182既有查询与目标接纳暴露为 JSON；HTML 管理页继续使用原行为。
// 只返回资料/指标，不返回日志和凭据；不是用户账号或公网准入系统。见 docs/client-library.md。
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func clientItem(i storage.ContentItem) map[string]any {
	if i.Kind == "creator" && i.Seq == 0 {
		i.Recorded = false
	}
	return map[string]any{"kind": i.Kind, "id": i.ID, "name": i.Name, "owner_id": i.OwnerID, "owner_name": i.OwnerName, "metric": i.Metric, "growth": i.Growth, "growth_from": i.GrowthFrom, "growth_to": i.GrowthTo, "seq": i.Seq, "observed_at": i.ObservedAt, "published_at": i.PublishedAt, "recorded": i.Recorded, "tracking": i.Following, "status": i.Status}
}

func (s *Server) clientLibrary(w http.ResponseWriter, r *http.Request, kind string) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	f := filter(r)
	f.Kind = kind
	f.ID = r.URL.Query().Get("id")
	page, err := s.reader.Catalog(ctx, s.platform, f, time.Now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取服务器资料。"})
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	s.states(page.Items)
	for _, i := range page.Items {
		items = append(items, clientItem(i))
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page.Page, "limit": page.Limit, "total": page.Total})
}

func (s *Server) clientDetail(w http.ResponseWriter, r *http.Request, kind, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	i, err := s.reader.Content(ctx, s.platform, kind, id, time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "服务器尚无该目标的成功观测记录。"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取服务器详情。"})
		return
	}
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		days = rangeDays(raw, -1)
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
	}
	if days < 0 || err != nil || page < 1 || page > 100000 {
		writeJSON(w, 400, map[string]string{"error": "时间范围或页码无效。"})
		return
	}
	versions, err := s.reader.Versions(ctx, s.platform, kind, id, "", 0, page, 10)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取版本。"})
		return
	}
	series, err := s.reader.Metrics(ctx, s.platform, kind, id, days, time.Now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取指标。"})
		return
	}
	entries := make([]map[string]any, 0, len(versions.Items))
	for _, v := range versions.Items {
		entries = append(entries, map[string]any{"seq": v.Seq, "at": v.At, "name": v.Name})
	}
	result := map[string]any{"item": clientItem(i), "metrics": series, "versions": entries, "version_page": versions.Page, "version_total": versions.Total, "version_limit": versions.Limit}
	if raw := r.URL.Query().Get("seq"); raw != "" {
		seq, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || seq < 1 || seq > i.Seq {
			writeJSON(w, 400, map[string]string{"error": "版本号无效。"})
			return
		}
		snapshot, e := s.reader.ReadSnapshot(ctx, s.platform, kind, id, seq)
		if e != nil {
			writeJSON(w, 404, map[string]string{"error": "没有该版本快照。"})
			return
		}
		result["snapshot"] = json.RawMessage(snapshot.Payload)
		result["snapshot_seq"] = snapshot.Seq
	}
	writeJSON(w, 200, result)
}

func (s *Server) clientTargetStatus(w http.ResponseWriter, r *http.Request) {
	kind, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	if !validID(kind, id) {
		writeJSON(w, 400, map[string]string{"error": "目标标识无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	t, exists, err := s.store.TrackingTarget(ctx, s.platform, kind, id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取跟踪状态。"})
		return
	}
	i, err := s.reader.Content(ctx, s.platform, kind, id, time.Now())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 500, map[string]string{"error": "无法读取资料状态。"})
		return
	}
	state := "not_recorded"
	if err == nil {
		state = i.Status
	}
	if exists && t.Enabled && err != nil {
		state = "pending"
	}
	if exists && !t.Enabled {
		state = "paused"
	}
	writeJSON(w, 200, map[string]any{"kind": kind, "id": id, "recorded": err == nil && i.Recorded && (kind != "creator" || i.Seq > 0), "tracking": exists && t.Enabled, "status": state})
}

func (s *Server) requestTarget(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) {
		return
	}
	var input struct {
		Kind string `json:"type"`
		ID   string `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ID = normalizeID(input.Kind, input.ID)
	if !validID(input.Kind, input.ID) {
		writeJSON(w, 400, map[string]string{"error": "目标标识无效。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	state, err := s.tracker.RequestTarget(ctx, input.Kind, input.ID)
	if err != nil {
		code, message := 502, "服务器无法验证或保存该目标，尚未确认加入跟踪。"
		switch {
		case errors.Is(err, tracker.ErrTargetBusy):
			code, message = 409, "目标正在采集中，请先刷新状态或稍后重试。"
		case errors.Is(err, tracker.ErrRequestCapacity):
			code, message = 429, "服务器采集任务较多，请稍后重试。"
		case errors.Is(err, tracker.ErrTargetPaused):
			code, message = 409, "目标已被服务器暂停，请联系管理员。"
		}
		writeJSON(w, code, map[string]string{"error": message})
		return
	}
	writeJSON(w, 200, map[string]any{"kind": input.Kind, "id": input.ID, "tracking": true, "status": state})
}
