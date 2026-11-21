// history.go 暴露观看历史同步与本地分页；写操作沿用 Agent 会话/来源/CSRF 保护。
// 历史只读本地缓存，不向服务器上传。见 docs/client-catalog.md。
package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) historyRoutes() {
	s.mux.HandleFunc("POST /api/history/sync", s.historySync)
	s.mux.HandleFunc("GET /api/history/videos", s.historyVideos)
	s.mux.HandleFunc("GET /api/history/creators", s.historyCreators)
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

func (s *Server) historyVideos(w http.ResponseWriter, r *http.Request) {
	days, daysErr := historyDays(r)
	page, pageErr := positivePage(r, "page")
	limit, limitErr := boundedLimit(r, "limit")
	if daysErr != nil || pageErr != nil || limitErr != nil {
		apiError(w, 400, "invalid_input", "历史范围或分页无效。")
		return
	}
	writeJSON(w, 200, s.service.HistoryVideos(days, page, limit))
}

func (s *Server) historyCreators(w http.ResponseWriter, r *http.Request) {
	days, daysErr := historyDays(r)
	page, pageErr := positivePage(r, "page")
	limit, limitErr := boundedLimit(r, "limit")
	if daysErr != nil || pageErr != nil || limitErr != nil {
		apiError(w, 400, "invalid_input", "历史范围或分页无效。")
		return
	}
	writeJSON(w, 200, s.service.HistoryCreators(days, page, limit))
}
