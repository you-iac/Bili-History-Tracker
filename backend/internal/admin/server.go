// server.go 实现后台路由、表单处理和页面渲染。

package admin

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/discovery"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

//go:embed templates/*.html assets/*
var templateFS embed.FS

// Server 渲染后台页面并转发操作到 storage 和 tracker。
type Server struct {
	store     *storage.Store
	reader    dashboardReader
	tracker   *tracker.Tracker
	log       *tracker.Log
	platform  string
	templates *template.Template
	discovery *discovery.Service
}

// New 解析模板并创建后台服务。
func New(store *storage.Store, trk *tracker.Tracker, log *tracker.Log, platform string) (*Server, error) {
	templates, err := template.New("").Funcs(templateFunctions()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse admin templates: %w", err)
	}
	var queries discovery.Queries
	if trk != nil {
		queries = trk
	}
	return &Server{store: store, reader: store, tracker: trk, log: log, platform: platform, templates: templates, discovery: discovery.New(store, queries, platform)}, nil
}

// Close 取消作者视频发现和导入任务，应在关闭数据库前调用。
func (s *Server) Close() { s.discovery.Close() }

// Handler 返回后台路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", s.overview)
	mux.HandleFunc("GET /videos", s.library)
	mux.HandleFunc("GET /creators", s.library)
	mux.HandleFunc("GET /manage", s.management)
	mux.HandleFunc("GET /activity", s.activity)
	mux.HandleFunc("GET /logs", s.logsPage)
	mux.HandleFunc("GET /detail/{type}/{id}", s.contentDetail)
	mux.HandleFunc("GET /api/metrics/{type}/{id}", s.metrics)
	mux.HandleFunc("GET /api/targets/status", s.targetStatus)
	mux.HandleFunc("GET /api/creators/{id}/videos", s.creatorVideos)
	mux.HandleFunc("POST /api/creators/{id}/videos/cancel", s.cancelCreatorVideos)
	mux.HandleFunc("POST /api/ingest/videos", s.ingestVideos)
	mux.HandleFunc("GET /api/ingest/jobs/{id}", s.ingestProgress)
	mux.HandleFunc("POST /api/ingest/jobs/{id}/{action}", s.ingestAction)
	assets, _ := fs.Sub(templateFS, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("POST /targets", s.addTarget)
	mux.HandleFunc("POST /targets/delete", s.deleteTarget)
	mux.HandleFunc("POST /targets/run", s.runTarget)
	mux.HandleFunc("POST /targets/interval", s.setInterval)
	return mux
}

func (s *Server) addTarget(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		s.requestTarget(w, r)
		return
	}
	targetType := strings.TrimSpace(r.FormValue("type"))
	id := normalizeID(targetType, r.FormValue("id"))
	if targetType != "creator" && targetType != "video" {
		s.redirect(w, r, "", "类型无效")
		return
	}
	if !validID(targetType, id) {
		s.redirect(w, r, "", "ID 格式无效：UP 主填写数字 UID，视频填写 BV 号")
		return
	}
	hours, err := strconv.ParseFloat(r.FormValue("interval_hours"), 64)
	if err != nil || math.IsNaN(hours) || math.IsInf(hours, 0) || hours < 0.1 || hours > 720 {
		hours = 3
	}
	interval := time.Duration(hours * float64(time.Hour))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	existed, err := s.store.HasTarget(ctx, s.platform, targetType, id)
	if err != nil {
		s.redirect(w, r, "", "无法读取关注状态，请稍后重试。")
		return
	}
	if err := s.store.AddTarget(ctx, s.platform, targetType, id, interval, time.Now()); err != nil {
		s.redirect(w, r, "", "保存失败："+err.Error())
		return
	}
	s.tracker.RunNow(storage.Target{
		Platform: s.platform, TargetType: targetType, TargetID: id,
		IntervalSeconds: int64(interval.Seconds()),
	})
	message := "已添加关注，正在进行首次采集：" + id
	if existed {
		message = "已关注该目标，已更新设置并触发刷新：" + id
	}
	if targetType == "creator" && strings.HasPrefix(r.FormValue("return_to"), "/manage") {
		r.Form.Set("return_to", "/manage?creator="+url.QueryEscape(id)+"#creator-videos")
		message += "；作者资料与视频关注独立，请在可添加视频中选择。"
	}
	s.redirect(w, r, message, "")
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
	if err != nil || math.IsNaN(hours) || math.IsInf(hours, 0) || hours < 0.1 || hours > 720 {
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
	destination := safeReturn(r.FormValue("return_to"))
	parsed, _ := url.Parse(destination)
	query := parsed.Query()
	query.Del("message")
	query.Del("error")
	if message != "" {
		query.Set("message", message)
	}
	if errorMessage != "" {
		query.Set("error", errorMessage)
	}
	parsed.RawQuery = query.Encode()
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errorMessage != "" {
			w.WriteHeader(400)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message, "error": errorMessage, "redirect": parsed.String()})
		return
	}
	http.Redirect(w, r, parsed.String(), http.StatusSeeOther)
}

func (s *Server) renderError(w http.ResponseWriter, err error) {
	http.Error(w, "暂时无法读取记录，请稍后重试。", http.StatusInternalServerError)
}

func validID(targetType, id string) bool {
	if targetType == "creator" {
		value, err := strconv.ParseInt(id, 10, 64)
		return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
	}
	return targetType == "video" && bvidInput.MatchString(id)
}

var bvidInput = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

// normalizeID 只本地提取固定 B站域名中的标识，不访问用户提供的 URL。
func normalizeID(kind, input string) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "http://") {
		u, err := url.Parse(input)
		if err != nil || u.User != nil || u.Port() != "" {
			return ""
		}
		host := strings.ToLower(u.Hostname())
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if kind == "creator" && host == "space.bilibili.com" && len(parts) == 1 {
			return parts[0]
		}
		if kind == "video" && (host == "www.bilibili.com" || host == "bilibili.com" || host == "m.bilibili.com") && len(parts) == 2 && parts[0] == "video" {
			return parts[1]
		}
		return ""
	}
	return input
}

func safeReturn(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/manage"
	}
	switch u.Path {
	case "/", "/manage", "/videos", "/creators", "/activity", "/logs":
		return u.String()
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "detail" && validID(parts[1], parts[2]) {
		return u.String()
	}
	return "/manage"
}
func urlQuery(value string) string { return url.QueryEscape(value) }

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
