// interactions.go 暴露互动自动关注设置和手动执行；写操作沿用本地会话/来源/CSRF 保护。
package httpapi

import (
	"encoding/json"
	"net/http"

	"bili-history-tracker/client/agent/internal/library"
)

func (s *Server) interactionRoutes() {
	s.mux.HandleFunc("GET /api/interactions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.library.InteractionSettings())
	})
	s.mux.HandleFunc("PUT /api/interactions", s.saveInteractions)
	s.mux.HandleFunc("POST /api/interactions/run", func(w http.ResponseWriter, r *http.Request) {
		if err := s.library.RunInteractionsOnce(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, s.library.InteractionSettings())
	})
}

func (s *Server) saveInteractions(w http.ResponseWriter, r *http.Request) {
	var cfg library.InteractionConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&cfg); err != nil {
		apiError(w, 400, "invalid_input", "互动设置无效。")
		return
	}
	saved, err := s.library.SetInteractionSettings(cfg)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, saved)
}
