// server.go 实现后台路由、表单处理和页面渲染。

package admin

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server 渲染后台页面并转发操作到 storage 和 tracker。
type Server struct {
	store     *storage.Store
	tracker   *tracker.Tracker
	log       *tracker.Log
	platform  string
	templates *template.Template
}

// New 解析模板并创建后台服务。
func New(store *storage.Store, trk *tracker.Tracker, log *tracker.Log, platform string) (*Server, error) {
	templates, err := template.New("").Funcs(template.FuncMap{
		"clock": func(at time.Time) string {
			if at.IsZero() {
				return "-"
			}
			return at.Local().Format("01-02 15:04:05")
		},
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse admin templates: %w", err)
	}
	return &Server{store: store, tracker: trk, log: log, platform: platform, templates: templates}, nil
}

// Handler 返回后台路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /detail/{type}/{id}", s.detail)
	mux.HandleFunc("POST /targets", s.addTarget)
	mux.HandleFunc("POST /targets/delete", s.deleteTarget)
	mux.HandleFunc("POST /targets/run", s.runTarget)
	mux.HandleFunc("POST /targets/interval", s.setInterval)
	return mux
}

type indexData struct {
	Counts  map[string]int64
	Targets []targetView
	Logs    []tracker.Entry
	Message string
	Error   string
	DBPath  string
}

type targetView struct {
	storage.Target
	TypeLabel    string
	IntervalText string
	NextRunText  string
	LastRunText  string
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	targets, err := s.store.ListTargets(ctx)
	if err != nil {
		s.renderError(w, err)
		return
	}
	counts, err := s.store.Counts(ctx)
	if err != nil {
		s.renderError(w, err)
		return
	}
	views := make([]targetView, 0, len(targets))
	for _, target := range targets {
		label := "UP 主"
		if target.TargetType == "video" {
			label = "视频"
		}
		lastRun := "-"
		if target.LastRunAt != nil {
			lastRun = time.UnixMilli(*target.LastRunAt).Local().Format("01-02 15:04:05")
		}
		views = append(views, targetView{
			Target:       target,
			TypeLabel:    label,
			IntervalText: formatInterval(target.IntervalSeconds),
			NextRunText:  time.UnixMilli(target.NextRunAt).Local().Format("01-02 15:04:05"),
			LastRunText:  lastRun,
		})
	}
	data := indexData{
		Counts:  counts,
		Targets: views,
		Logs:    s.log.Recent(100),
		Message: r.URL.Query().Get("message"),
		Error:   r.URL.Query().Get("error"),
		DBPath:  s.store.Path(),
	}
	_ = s.templates.ExecuteTemplate(w, "index.html", data)
}

type detailData struct {
	Title   string
	Subtitle string
	Meta    [][2]string
	Payload string
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	kind := r.PathValue("type")
	id := r.PathValue("id")
	switch kind {
	case "video":
		detail, err := s.store.LatestVideo(ctx, s.platform, id)
		if err != nil {
			s.renderError(w, err)
			return
		}
		meta := [][2]string{
			{"视频 ID", detail.VideoID},
			{"标题", detail.Title},
			{"播放量", formatInt(detail.ViewCount)},
			{"投稿时间", formatMillis(detail.PublishedAt)},
			{"首次观测", formatMillis(&detail.FirstSeenAt)},
			{"最近观测", formatMillis(&detail.LastObservedAt)},
			{"当前版本", strconv.FormatInt(detail.CurrentSeq, 10)},
			{"版本观测时间", formatMillis(&detail.ObservedAt)},
			{"来源", detail.SourceURL},
		}
		_ = s.templates.ExecuteTemplate(w, "detail.html", detailData{
			Title: "视频详情", Subtitle: detail.Title, Meta: meta, Payload: prettyJSON(detail.Payload),
		})
	case "creator":
		detail, err := s.store.LatestCreator(ctx, s.platform, id)
		if err != nil {
			s.renderError(w, err)
			return
		}
		meta := [][2]string{
			{"作者 ID", detail.CreatorID},
			{"昵称", detail.Name},
			{"粉丝数", formatInt(detail.FollowerCount)},
			{"首次观测", formatMillis(&detail.FirstSeenAt)},
			{"最近引用", formatMillis(&detail.LastReferencedAt)},
			{"资料观测", formatMillis(detail.ProfileObservedAt)},
			{"当前版本", formatInt64Ptr(detail.CurrentSeq)},
			{"版本观测时间", formatMillis(detail.ObservedAt)},
			{"来源", detail.SourceURL},
		}
		_ = s.templates.ExecuteTemplate(w, "detail.html", detailData{
			Title: "作者详情", Subtitle: detail.Name, Meta: meta, Payload: prettyJSON(detail.Payload),
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) addTarget(w http.ResponseWriter, r *http.Request) {
	targetType := strings.TrimSpace(r.FormValue("type"))
	id := strings.TrimSpace(r.FormValue("id"))
	if targetType != "creator" && targetType != "video" {
		s.redirect(w, r, "", "类型无效")
		return
	}
	if !validID(targetType, id) {
		s.redirect(w, r, "", "ID 格式无效：UP 主填写数字 UID，视频填写 BV 号")
		return
	}
	hours, err := strconv.ParseFloat(r.FormValue("interval_hours"), 64)
	if err != nil || hours < 0.1 || hours > 720 {
		hours = 3
	}
	interval := time.Duration(hours * float64(time.Hour))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.AddTarget(ctx, s.platform, targetType, id, interval, time.Now()); err != nil {
		s.redirect(w, r, "", "保存失败："+err.Error())
		return
	}
	s.tracker.RunNow(storage.Target{
		Platform: s.platform, TargetType: targetType, TargetID: id,
		IntervalSeconds: int64(interval.Seconds()),
	})
	s.redirect(w, r, "已添加并开始查询："+id, "")
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	targetType := r.FormValue("type")
	id := r.FormValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.RemoveTarget(ctx, s.platform, targetType, id); err != nil {
		s.redirect(w, r, "", "删除失败："+err.Error())
		return
	}
	s.redirect(w, r, "已停止跟踪 "+id+"（已保存的历史不会删除）", "")
}

func (s *Server) runTarget(w http.ResponseWriter, r *http.Request) {
	targetType := r.FormValue("type")
	id := r.FormValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	targets, err := s.store.ListTargets(ctx)
	if err != nil {
		s.redirect(w, r, "", "查询失败："+err.Error())
		return
	}
	for _, target := range targets {
		if target.TargetType == targetType && target.TargetID == id {
			s.tracker.RunNow(target)
			s.redirect(w, r, "已触发查询："+id, "")
			return
		}
	}
	s.redirect(w, r, "", "目标不在跟踪列表中")
}

func (s *Server) setInterval(w http.ResponseWriter, r *http.Request) {
	targetType := r.FormValue("type")
	id := r.FormValue("id")
	hours, err := strconv.ParseFloat(r.FormValue("interval_hours"), 64)
	if err != nil || hours < 0.1 || hours > 720 {
		s.redirect(w, r, "", "间隔必须是 0.1 到 720 小时")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.SetTargetInterval(ctx, s.platform, targetType, id, time.Duration(hours*float64(time.Hour))); err != nil {
		s.redirect(w, r, "", "更新失败："+err.Error())
		return
	}
	s.redirect(w, r, "已更新查询间隔", "")
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, message, errorMessage string) {
	query := "?"
	if message != "" {
		query += "message=" + template.URLQueryEscaper(message)
	}
	if errorMessage != "" {
		if query != "?" {
			query += "&"
		}
		query += "error=" + template.URLQueryEscaper(errorMessage)
	}
	http.Redirect(w, r, "/"+query, http.StatusSeeOther)
}

func (s *Server) renderError(w http.ResponseWriter, err error) {
	http.Error(w, "后台查询失败: "+err.Error(), http.StatusInternalServerError)
}

func validID(targetType, id string) bool {
	if targetType == "creator" {
		value, err := strconv.ParseInt(id, 10, 64)
		return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
	}
	return strings.HasPrefix(id, "BV") && len(id) == 12
}

func formatInterval(seconds int64) string {
	duration := time.Duration(seconds) * time.Second
	if duration%time.Hour == 0 {
		return strconv.FormatInt(int64(duration.Hours()), 10) + " 小时"
	}
	return duration.String()
}

func formatMillis(value *int64) string {
	if value == nil || *value == 0 {
		return "-"
	}
	return time.UnixMilli(*value).Local().Format("2006-01-02 15:04:05")
}

func formatInt(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}

func formatInt64Ptr(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}

func prettyJSON(raw []byte) string {
	if len(raw) == 0 {
		return "（没有完整资料快照）"
	}
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buffer.String()
}
