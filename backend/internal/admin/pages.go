// pages.go 编排观察台的只读页面和指标接口，不通过页面访问触发上游采集。
// 聚合依赖最小读取接口，管理命令继续走原有 Store/Tracker；见 docs/dashboard.md。
package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

type dashboardReader interface {
	Catalog(context.Context, string, storage.CatalogFilter, time.Time) (storage.ContentPage, error)
	Content(context.Context, string, string, string, time.Time) (storage.ContentItem, error)
	Summary(context.Context, string, time.Time) (storage.DashboardSummary, error)
	Metrics(context.Context, string, string, string, int, time.Time) (storage.MetricSeries, error)
	CollectionSeries(context.Context, string, time.Time) (storage.MetricSeries, error)
	Versions(context.Context, string, string, string, string, int, int, int) (storage.VersionPage, error)
	ReadSnapshot(context.Context, string, string, string, int64) (storage.Snapshot, error)
}

func (s *Server) base(r *http.Request, nav, title, subtitle string) viewData {
	return viewData{Nav: nav, Title: title, Subtitle: subtitle, Message: r.URL.Query().Get("message"), Error: r.URL.Query().Get("error"), Q: strings.TrimSpace(r.URL.Query().Get("q")), Sort: r.URL.Query().Get("sort"), Following: r.URL.Query().Get("following"), Kind: r.URL.Query().Get("kind"), Tab: r.URL.Query().Get("tab"), Days: r.URL.Query().Get("days"), ReturnURL: r.URL.RequestURI()}
}
func (s *Server) render(w http.ResponseWriter, name string, data viewData) {
	var buffer bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buffer, name, data); err != nil {
		http.Error(w, "页面暂时无法加载，请稍后重试。", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buffer.WriteTo(w)
}
func (s *Server) states(items []storage.ContentItem) bool {
	pending := false
	for i := range items {
		if s.tracker != nil && s.tracker.IsRunning(items[i].Kind, items[i].ID) {
			items[i].Status = "running"
		}
		if items[i].Status == "pending" || items[i].Status == "running" {
			pending = true
		}
	}
	return pending
}
func filter(r *http.Request) storage.CatalogFilter {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return storage.CatalogFilter{Kind: r.URL.Query().Get("kind"), Q: strings.TrimSpace(r.URL.Query().Get("q")), Sort: r.URL.Query().Get("sort"), Following: r.URL.Query().Get("following"), Page: page, Limit: 18}
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	now := time.Now()
	data := s.base(r, "overview", "信息总览", "关注内容，观察变化。")
	var err error
	data.Summary, err = s.reader.Summary(ctx, s.platform, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	creatorSort := "popular"
	data.CreatorRank = r.URL.Query().Get("rank")
	if data.CreatorRank == "growth" {
		creatorSort = "growth"
	}
	videoSort := "popular"
	data.VideoRank = r.URL.Query().Get("video_rank")
	if data.VideoRank == "growth" {
		videoSort = "growth"
	}
	creators, err := s.reader.Catalog(ctx, s.platform, storage.CatalogFilter{Kind: "creator", Following: "yes", Sort: creatorSort, Limit: 5, KnownOnly: true}, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Creators = creators.Items
	videos, err := s.reader.Catalog(ctx, s.platform, storage.CatalogFilter{Kind: "video", Sort: videoSort, Limit: 6, KnownOnly: true}, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Videos = videos.Items
	issues, err := s.reader.Catalog(ctx, s.platform, storage.CatalogFilter{OnlyTargets: true, State: "issues", Limit: 4}, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Issues = issues.Items
	data.Versions, err = s.reader.Versions(ctx, s.platform, "", "", "", 7, 1, 5)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Series, err = s.reader.CollectionSeries(ctx, s.platform, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.SeriesJSON = seriesJSON(data.Series)
	s.states(data.Videos)
	s.states(data.Creators)
	s.render(w, "overview", data)
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	kind, title, subtitle, nav := "video", "视频库", "每一条视频，都有自己的记录。", "videos"
	if r.URL.Path == "/creators" {
		kind, title, subtitle, nav = "creator", "UP 主库", "在已记录的内容中，发现创作与成长。", "creators"
	}
	data := s.base(r, nav, title, subtitle)
	data.Kind = kind
	f := filter(r)
	f.Kind = kind
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.reader.Catalog(ctx, s.platform, f, time.Now())
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Items = page.Items
	data.Page = makePager(r.URL, page.Page, page.Limit, page.Total, "page")
	s.states(data.Items)
	s.render(w, "library", data)
}

func (s *Server) management(w http.ResponseWriter, r *http.Request) {
	data := s.base(r, "manage", "关注管理", "添加你关心的视频和 UP 主，让变化持续被记录。")
	f := filter(r)
	f.OnlyTargets = true
	f.Limit = 20
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.reader.Catalog(ctx, s.platform, f, time.Now())
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.Items = page.Items
	data.Page = makePager(r.URL, page.Page, page.Limit, page.Total, "page")
	data.HasPending = s.states(data.Items)
	s.render(w, "manage", data)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	data := s.base(r, "activity", "动态与提醒", "内容变化和采集情况，各自清晰记录。")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	f := filter(r)
	f.Limit = 20
	if data.Tab == "issues" || data.Tab == "recovered" {
		f.OnlyTargets = true
		f.State = data.Tab
		page, err := s.reader.Catalog(ctx, s.platform, f, time.Now())
		if err != nil {
			s.renderError(w, err)
			return
		}
		data.Items = page.Items
		data.Page = makePager(r.URL, page.Page, page.Limit, page.Total, "page")
		s.states(data.Items)
	} else {
		data.Tab = "updates"
		days := rangeDays(data.Days, 7)
		versions, err := s.reader.Versions(ctx, s.platform, data.Kind, "", data.Q, days, f.Page, 20)
		if err != nil {
			s.renderError(w, err)
			return
		}
		data.Versions = versions
		data.Page = makePager(r.URL, versions.Page, versions.Limit, versions.Total, "page")
	}
	s.render(w, "activity", data)
}

func rangeDays(value string, fallback int) int {
	switch value {
	case "7":
		return 7
	case "30":
		return 30
	case "90":
		return 90
	case "all":
		return 0
	}
	return fallback
}

func (s *Server) contentDetail(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	if !validID(kind, id) || (kind != "video" && kind != "creator") {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	now := time.Now()
	item, err := s.reader.Content(ctx, s.platform, kind, id, now)
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/manage?message="+urlQuery("该目标尚未采集成功，请查看关注管理中的状态。"), http.StatusSeeOther)
		return
	}
	if err != nil {
		s.renderError(w, err)
		return
	}
	data := s.base(r, "videos", item.Name, "从首次记录开始，回看每一次变化。")
	if kind == "creator" {
		data.Nav = "creators"
	}
	data.Kind = kind
	data.Item = item
	items := []storage.ContentItem{item}
	s.states(items)
	data.Item = items[0]
	seq := int64(0)
	if raw := r.URL.Query().Get("seq"); raw != "" {
		seq, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || seq < 1 || seq > item.Seq {
			http.Error(w, "版本号无效。", 400)
			return
		}
	}
	data.Snapshot, err = s.reader.ReadSnapshot(ctx, s.platform, kind, id, seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.renderError(w, err)
		return
	}
	if err != nil && seq > 0 {
		http.NotFound(w, r)
		return
	}
	data.Payload = prettyJSON(data.Snapshot.Payload)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data.Snapshot.Payload, &raw)
	if kind == "video" {
		_ = json.Unmarshal(raw["desc"], &data.Description)
	} else {
		var card struct {
			Sign string `json:"sign"`
		}
		_ = json.Unmarshal(raw["card"], &card)
		data.Description = card.Sign
	}
	if data.Snapshot.Seq > 1 {
		previous, e := s.reader.ReadSnapshot(ctx, s.platform, kind, id, data.Snapshot.Seq-1)
		if e != nil {
			s.renderError(w, e)
			return
		}
		data.Changes = contentChanges(kind, previous.Payload, data.Snapshot.Payload)
	}
	data.Series, err = s.reader.Metrics(ctx, s.platform, kind, id, rangeDays(data.Days, 30), now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.SeriesJSON = seriesJSON(data.Series)
	lifetime, err := s.reader.Metrics(ctx, s.platform, kind, id, 0, now)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if len(lifetime.Points) >= 2 {
		first, last := lifetime.Points[0], lifetime.Points[len(lifetime.Points)-1]
		delta := last.Value - first.Value
		data.Lifetime = &delta
		data.LifetimeFrom = first.At
		data.LifetimeTo = last.At
	}
	vpage, _ := strconv.Atoi(r.URL.Query().Get("page"))
	data.Versions, err = s.reader.Versions(ctx, s.platform, kind, id, "", 0, vpage, 10)
	if err != nil {
		s.renderError(w, err)
		return
	}
	data.VersionPage = makePager(r.URL, data.Versions.Page, data.Versions.Limit, data.Versions.Total, "page")
	if kind == "creator" {
		vp, _ := strconv.Atoi(r.URL.Query().Get("vp"))
		sort := r.URL.Query().Get("video_sort")
		if sort != "popular" {
			sort = "published"
		}
		data.RelatedSort = sort
		related, e := s.reader.Catalog(ctx, s.platform, storage.CatalogFilter{Kind: "video", OwnerID: id, Sort: sort, Page: vp, Limit: 6}, now)
		if e != nil {
			s.renderError(w, e)
			return
		}
		data.Videos = related.Items
		data.RelatedPage = makePager(r.URL, related.Page, related.Limit, related.Total, "vp")
	}
	s.render(w, "detail", data)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	if !validID(kind, id) || (kind != "video" && kind != "creator") {
		http.NotFound(w, r)
		return
	}
	days := rangeDays(r.URL.Query().Get("days"), -1)
	if days < 0 {
		http.Error(w, "时间范围无效。", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := s.reader.Content(ctx, s.platform, kind, id, time.Now()); err != nil {
		http.NotFound(w, r)
		return
	}
	series, err := s.reader.Metrics(ctx, s.platform, kind, id, days, time.Now())
	if err != nil {
		s.renderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(series)
}

func (s *Server) targetStatus(w http.ResponseWriter, r *http.Request) {
	f := filter(r)
	f.OnlyTargets = true
	f.Limit = 20
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.reader.Catalog(ctx, s.platform, f, time.Now())
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.states(page.Items)
	type status struct{ Key, Name, State, Label, Reason, Last, Next string }
	items := []status{}
	for _, i := range page.Items {
		items = append(items, status{Key: i.Kind + ":" + i.ID, Name: i.Name, State: i.Status, Label: statusLabel(i.Status), Reason: reasonLabel(i.LastResult), Last: clock(i.LastSuccessAt), Next: clock(i.NextRunAt)})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(items)
}

func (s *Server) logsPage(w http.ResponseWriter, r *http.Request) {
	data := s.base(r, "logs", "采集记录", "最近可恢复的查询日志 · 最多 500 条，不代表全部历史。")
	if s.log != nil {
		entries := s.log.Recent(500)
		for _, e := range entries {
			target := strings.SplitN(e.Target, ":", 2)
			kind, id := "", ""
			if len(target) == 2 {
				kind, id = target[0], target[1]
			}
			if data.Kind != "" && data.Kind != kind {
				continue
			}
			if data.Tab != "" && data.Tab != e.Level {
				continue
			}
			if data.Q != "" && !strings.Contains(strings.ToLower(e.Target+" "+e.Detail+" "+e.Message), strings.ToLower(data.Q)) {
				continue
			}
			result := reasonLabel(e.Message)
			if e.Action == "discover" && e.Message == "ok" {
				result = "发现完成"
			}
			data.Logs = append(data.Logs, logView{At: e.Time.UnixMilli(), Kind: kind, ID: id, Level: e.Level, Action: e.Action, Result: result, Detail: e.Detail, Duration: e.DurationMS})
			data.LogSince = e.Time.UnixMilli()
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total := len(data.Logs)
	page = min(page, max(1, (total+24)/25))
	data.Page = makePager(r.URL, page, 25, total, "page")
	start := (page - 1) * 25
	if start < total {
		data.Logs = data.Logs[start:min(start+25, total)]
	} else {
		data.Logs = nil
	}
	s.render(w, "logs", data)
}
