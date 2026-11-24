// history.go 暴露观看历史同步与本地分页；写操作沿用 Agent 会话/来源/CSRF 保护。
// 历史只读本地缓存，不向服务器上传。见 docs/client-catalog.md。
package httpapi

import (
	"net/http"
	"strconv"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/scanner"
)

// 关注状态与头像只从本机资料缓存补齐，不因浏览历史查询平台或公共服务器。
type historyVideoItem struct {
	bilibili.HistoryItem
	Followed bool `json:"followed"`
}

type historyCreatorItem struct {
	scanner.HistoryCreator
	Followed bool   `json:"followed"`
	Cover    string `json:"cover,omitempty"`
}

func (s *Server) historyRoutes() {
	s.mux.HandleFunc("POST /api/history/sync", s.historySync)
	s.mux.HandleFunc("POST /api/history/interactions", s.historyInteractions)
	s.mux.HandleFunc("GET /api/history/videos", s.historyVideos)
	s.mux.HandleFunc("GET /api/history/creators", s.historyCreators)
}

func (s *Server) historyInteractions(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.CheckInteractions(r.Context(), 60)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) historySync(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.HistorySync(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func historyDays(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("days")
	if raw == "" {
		return 30, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || (days != 3 && days != 7 && days != 30) {
		return 0, strconv.ErrRange
	}
	return days, nil
}

// 未关注指本地未关注，待服务器确认的本地关注同样排除；不查询远程跟踪状态。
func (s *Server) historyFilter(r *http.Request, kind string) (func(string) bool, error) {
	switch r.URL.Query().Get("unfollowed") {
	case "", "0":
		return nil, nil
	case "1":
		return func(id string) bool {
			cached, _ := s.library.CurrentCache(kind, id)
			return cached.LocalFollow == nil
		}, nil
	default:
		return nil, strconv.ErrSyntax
	}
}

func (s *Server) historyVideos(w http.ResponseWriter, r *http.Request) {
	days, daysErr := historyDays(r)
	page, pageErr := positivePage(r, "page")
	limit, limitErr := boundedLimit(r, "limit")
	include, filterErr := s.historyFilter(r, "video")
	if daysErr != nil || pageErr != nil || limitErr != nil || filterErr != nil {
		apiError(w, 400, "invalid_input", "历史范围、筛选或分页无效。")
		return
	}
	data := s.service.HistoryVideosFiltered(days, page, limit, include)
	items := make([]historyVideoItem, 0, len(data.Items))
	for _, item := range data.Items {
		cached, _ := s.library.CurrentCache("video", item.BVID)
		items = append(items, historyVideoItem{HistoryItem: item, Followed: cached.LocalFollow != nil})
	}
	writeJSON(w, 200, struct {
		scanner.HistoryVideoPage
		Items []historyVideoItem `json:"items"`
	}{data, items})
}

func (s *Server) historyCreators(w http.ResponseWriter, r *http.Request) {
	days, daysErr := historyDays(r)
	page, pageErr := positivePage(r, "page")
	limit, limitErr := boundedLimit(r, "limit")
	include, filterErr := s.historyFilter(r, "creator")
	if daysErr != nil || pageErr != nil || limitErr != nil || filterErr != nil {
		apiError(w, 400, "invalid_input", "历史范围、筛选或分页无效。")
		return
	}
	data := s.service.HistoryCreatorsFiltered(days, page, limit, include)
	items := make([]historyCreatorItem, 0, len(data.Items))
	for _, item := range data.Items {
		cached, _ := s.library.CurrentCache("creator", item.ID)
		items = append(items, historyCreatorItem{HistoryCreator: item, Followed: cached.LocalFollow != nil, Cover: cached.Item.Cover})
	}
	writeJSON(w, 200, struct {
		scanner.HistoryCreatorPage
		Items []historyCreatorItem `json:"items"`
	}{data, items})
}
