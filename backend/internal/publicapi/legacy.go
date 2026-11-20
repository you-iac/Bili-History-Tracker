// legacy.go 将既有实时查询的上游业务失败适配为公共JSON，不改变查询内核或管理端。
// 保留非法请求和未知路由的HTTP语义。见 docs/public-response.md。
package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type legacyResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *legacyResponse) Header() http.Header { return w.header }
func (w *legacyResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *legacyResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(data)
}
func (s *Server) legacyJSON(w http.ResponseWriter, r *http.Request) {
	capture := &legacyResponse{header: make(http.Header)}
	s.legacy.ServeHTTP(capture, r)
	var fields map[string]json.RawMessage
	if json.Unmarshal(capture.body.Bytes(), &fields) != nil {
		for k, v := range capture.header {
			w.Header()[k] = v
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return
	}
	if failure, exists := fields["error"]; exists && capture.status != 400 {
		business(w, "UPSTREAM_UNAVAILABLE", "平台暂未返回可用资料，原因未确认。", map[string]any{"upstream_error": failure})
		return
	}
	writeJSON(w, capture.status, fields)
}
