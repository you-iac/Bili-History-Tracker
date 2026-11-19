// Package publicapi 组装 8181 公共 JSON 路由、进程级限流和有界目标申请。
// 资料读取复用 storage，目标接纳委托 tracker，实时查询委托 httpapi；
// 不导入 admin，不暴露管理表单、日志或批量导入，不接收本地 Cookie。
// 正常业务HTTP200并检查JSON结果；入口限制不等于长期预算或认证。见 docs/public-response.md、docs/architecture.md。
// 申请阶段预算及有界收尾见 docs/request-stages.md。
// detail_sources.go 的独立历史/资料回退不调用目录聚合，见 docs/client-detail-data-sources.md。
package publicapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/httpapi"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

// Config 只包含全局每秒请求数和批量截断位置；零值使用默认值，不代表无限制。
type Config struct {
	RequestsPerSecond int
	BatchMax          int
}

// targetLookup 只读取准确的调度状态，区分暂停、缺失和读取错误。
type targetLookup interface {
	TrackingTarget(ctx context.Context, platform, kind, id string) (storage.Target, bool, error)
}

// Server 与后台共享存储和采集实例，只注册明确允许公开的操作。
type Server struct {
	store            targetLookup
	reader           *storage.Store
	tracker          *tracker.Tracker
	platform         string
	legacy           http.Handler
	config           Config
	admissionTimeout time.Duration
	confirmTimeout   time.Duration
}

// New 检查配置后创建公共入口；Handler 应在启动时调用一次并供所有连接共享。
func New(store *storage.Store, trk *tracker.Tracker, queries httpapi.Querier, platform string, cfg Config) (*Server, error) {
	if cfg.RequestsPerSecond == 0 {
		cfg.RequestsPerSecond = 20
	}
	if cfg.BatchMax == 0 {
		cfg.BatchMax = 10
	}
	if cfg.RequestsPerSecond < 1 || cfg.RequestsPerSecond > 10000 || cfg.BatchMax < 1 || cfg.BatchMax > 100 {
		return nil, fmt.Errorf("public API limits out of range")
	}
	if store == nil || trk == nil || queries == nil || platform == "" {
		return nil, fmt.Errorf("public API requires storage, tracker, queries and platform")
	}
	return &Server{store: store, reader: store, tracker: trk, platform: platform, legacy: httpapi.New(queries), config: cfg, admissionTimeout: 25 * time.Second, confirmTimeout: 3 * time.Second}, nil
}

// Handler 返回一个共享限额的路由树；不按 IP 分配额度，不挂载后台处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /{$}", s.clientOverview)
	mux.HandleFunc("GET /creators", func(w http.ResponseWriter, r *http.Request) { s.clientLibrary(w, r, "creator") })
	mux.HandleFunc("GET /videos", func(w http.ResponseWriter, r *http.Request) { s.clientLibrary(w, r, "video") })
	mux.HandleFunc("GET /detail/{type}/{id}", func(w http.ResponseWriter, r *http.Request) {
		kind, id := r.PathValue("type"), r.PathValue("id")
		if !validID(kind, id) {
			writeJSON(w, 400, map[string]string{"error": "目标标识无效。"})
			return
		}
		s.clientDetail(w, r, kind, id)
	})
	mux.HandleFunc("GET /api/targets/status", s.clientTargetStatus)
	mux.HandleFunc("GET /api/metrics/{type}/{id}", s.metrics)
	mux.HandleFunc("GET /api/detail/{type}/{id}", s.detailHistory)
	mux.HandleFunc("GET /api/detail/{type}/{id}/{part}", s.detailSources)
	mux.HandleFunc("POST /targets", s.requestTarget)
	mux.HandleFunc("/api/v1/", s.legacyJSON)
	return newGate(s.config.RequestsPerSecond).wrap(mux)
}

func filter(r *http.Request) storage.CatalogFilter {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return storage.CatalogFilter{Q: strings.TrimSpace(r.URL.Query().Get("q")), Sort: r.URL.Query().Get("sort"), Following: r.URL.Query().Get("following"), Page: page, Limit: 18}
}

func (s *Server) states(items []storage.ContentItem) {
	for i := range items {
		if s.tracker.IsRunning(items[i].Kind, items[i].ID) {
			items[i].Status = "running"
		}
	}
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
	default:
		return fallback
	}
}
