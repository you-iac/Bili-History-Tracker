// detail_sources.go 为当前资料、缓存、历史和按需快照提供独立同源入口。
// 各分支可独立失败，读取不申请跟踪。见 docs/client-detail-data-sources.md。
package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) detailSourceRoutes() {
	s.mux.HandleFunc("GET /api/current/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.library.Current(r.Context(), r.PathValue("kind"), r.PathValue("id"), r.URL.Query().Get("refresh") == "1")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	s.mux.HandleFunc("GET /api/current/{kind}/{id}/cache", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.library.CurrentCache(r.PathValue("kind"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	s.mux.HandleFunc("GET /api/history/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		days := r.URL.Query().Get("days")
		if days == "" {
			days = "30"
		}
		v, err := s.library.History(r.Context(), r.PathValue("kind"), r.PathValue("id"), days)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	s.mux.HandleFunc("GET /api/history/{kind}/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		before := int64(0)
		if raw := r.URL.Query().Get("before"); raw != "" {
			var err error
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 1 {
				apiError(w, 400, "invalid_input", "版本游标无效。")
				return
			}
		}
		v, err := s.library.Versions(r.Context(), r.PathValue("kind"), r.PathValue("id"), before)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	s.mux.HandleFunc("GET /api/history/{kind}/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		seq, err := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
		if err != nil || seq < 1 {
			apiError(w, 400, "invalid_input", "版本号无效。")
			return
		}
		v, err := s.library.Snapshot(r.Context(), r.PathValue("kind"), r.PathValue("id"), seq)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
}
