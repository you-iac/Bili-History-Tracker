// library.go 将本地关注和远程读取接入已有会话/来源/CSRF 保护，不将平台 Cookie 传给服务器。
// 提供公开工作台、自动关注和受保护的目录设置；见 docs/client-library.md、docs/client-workspace.md、docs/client-catalog.md。
package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) libraryRoutes() {
	s.mux.HandleFunc("GET /api/images/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		seq := int64(0)
		var err error
		if raw := r.URL.Query().Get("seq"); raw != "" {
			seq, err = strconv.ParseInt(raw, 10, 64)
		}
		if err != nil || seq < 0 {
			apiError(w, 400, "invalid_input", "图片版本无效。")
			return
		}
		result, err := s.library.Image(r.Context(), r.PathValue("kind"), r.PathValue("id"), seq)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	s.mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.store.DirectoryInfo()) })
	s.mux.HandleFunc("PUT /api/settings/directory", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Directory string `json:"data_directory"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := s.store.SwitchDirectory(input.Directory); err != nil {
			apiError(w, 400, "directory_error", err.Error())
			return
		}
		writeJSON(w, 200, s.store.DirectoryInfo())
	})
	s.mux.HandleFunc("GET /api/remote/connection", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"server_url": s.library.Connection()})
	})
	s.mux.HandleFunc("PUT /api/remote/connection", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			URL string `json:"server_url"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := s.library.Configure(input.URL); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"server_url": s.library.Connection()})
	})
	s.mux.HandleFunc("POST /api/remote/test", func(w http.ResponseWriter, r *http.Request) {
		if err := s.library.Test(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("GET /api/follows", s.follows)
	s.mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.library.Dashboard()) })
	s.mux.HandleFunc("POST /api/overview/refresh", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.library.RefreshOverview(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, data)
	})
	s.mux.HandleFunc("GET /api/remote/catalog", func(w http.ResponseWriter, r *http.Request) {
		page, err := positivePage(r, "page")
		q := r.URL.Query().Get("q")
		if err != nil || len(q) > 300 {
			apiError(w, 400, "invalid_input", "搜索条件或页码无效。")
			return
		}
		data, err := s.library.Search(r.Context(), r.URL.Query().Get("kind"), q, page)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, data)
	})
	s.mux.HandleFunc("POST /api/follows", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		if !decode(w, r, &input) {
			return
		}
		f, err := s.library.Follow(r.Context(), input.Kind, input.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, f)
	})
	s.mux.HandleFunc("DELETE /api/follows/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.library.Remove(r.PathValue("kind"), r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("POST /api/follows/{kind}/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		f, err := s.library.Refresh(r.Context(), r.PathValue("kind"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, f)
	})
	s.mux.HandleFunc("POST /api/follows/{kind}/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
		f, err := s.library.Submit(r.Context(), r.PathValue("kind"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, f)
	})
	s.mux.HandleFunc("GET /api/library/{kind}/{id}", s.libraryDetail)
}
func (s *Server) follows(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	page, err := positivePage(r, "page")
	if err != nil || (kind != "" && kind != "creator" && kind != "video") {
		apiError(w, 400, "invalid_input", "关注类型或页码无效。")
		return
	}
	writeJSON(w, 200, s.library.ListFiltered(kind, r.URL.Query().Get("q"), page, r.URL.Query().Get("pending") == "1"))
}
func positivePage(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err == nil && (n < 1 || n > 100000) {
		return 0, strconv.ErrRange
	}
	return n, err
}
func (s *Server) libraryDetail(w http.ResponseWriter, r *http.Request) {
	page, err := positivePage(r, "page")
	seq := int64(0)
	if raw := r.URL.Query().Get("seq"); raw != "" && err == nil {
		seq, err = strconv.ParseInt(raw, 10, 64)
		if seq < 1 {
			err = strconv.ErrRange
		}
	}
	if err != nil {
		apiError(w, 400, "invalid_input", "版本或页码无效。")
		return
	}
	detail, err := s.library.Detail(r.Context(), r.PathValue("kind"), r.PathValue("id"), page, seq, r.URL.Query().Get("days"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, detail)
}
