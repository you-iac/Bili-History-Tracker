// discovery.go 提供作者视频的统一分页与显式导入 API，不解释平台发现方式。
// 本期沿用本地单管理员工作区；JSON 写入仅接受同源请求，外部扩展认证尚未接入。
// 设计说明：docs/video-discovery.md。
package admin

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"bili-history-tracker/backend/internal/discovery"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func discoveryError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "暂时无法读取或保存记录。"
	if errors.Is(err, discovery.ErrInput) {
		status, message = 400, "参数无效，请检查作者、分页范围或所选视频（每次最多 100 个）。"
	}
	if errors.Is(err, discovery.ErrExpired) {
		status, message = 410, "列表或任务已过期，请重新加载。"
	}
	if errors.Is(err, discovery.ErrCapacity) {
		status, message = 429, "当前任务较多，请稍后重试。"
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func (s *Server) checkPlatform(w http.ResponseWriter, r *http.Request) bool {
	if p := r.URL.Query().Get("platform"); p != "" && p != s.platform {
		writeJSON(w, 400, map[string]string{"error": "当前工作区不支持该平台。"})
		return false
	}
	return true
}
func (s *Server) creatorVideos(w http.ResponseWriter, r *http.Request) {
	if !s.checkPlatform(w, r) {
		return
	}
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			discoveryError(w, discovery.ErrInput)
			return
		}
		limit = value
	}
	creator := r.PathValue("id")
	var page discovery.Page
	var err error
	switch r.URL.Query().Get("scope") {
	case "", "recorded":
		page, err = s.discovery.Recorded(r.Context(), creator, r.URL.Query().Get("cursor"), limit)
	case "available":
		page, err = s.discovery.List(r.Context(), creator, r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("retry") == "1")
	default:
		err = discovery.ErrInput
	}
	if err != nil {
		discoveryError(w, err)
		return
	}
	writeJSON(w, 200, page)
}

// JSON 与同源校验阻止第三方网页利用本地后台写入；不将工作区语义当成真实登录。
func sameOriginJSON(w http.ResponseWriter, r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeJSON(w, 415, map[string]string{"error": "请使用 JSON 请求。"})
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSON(w, 403, map[string]string{"error": "仅允许本工作区发起操作。"})
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeJSON(w, 403, map[string]string{"error": "请求来源无效。"})
			return false
		}
	}
	return true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "JSON 请求格式无效。"})
		return false
	}
	return true
}
func (s *Server) ingestVideos(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) || !s.checkPlatform(w, r) {
		return
	}
	var request struct {
		discovery.ImportRequest
		Platform string `json:"platform"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Platform != "" && request.Platform != s.platform {
		discoveryError(w, discovery.ErrInput)
		return
	}
	job, err := s.discovery.Start(request.ImportRequest)
	if err != nil {
		discoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
func (s *Server) ingestProgress(w http.ResponseWriter, r *http.Request) {
	job, err := s.discovery.Progress(r.PathValue("id"))
	if err != nil {
		discoveryError(w, err)
		return
	}
	writeJSON(w, 200, job)
}
func (s *Server) ingestAction(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) {
		return
	}
	var job discovery.Progress
	var err error
	switch r.PathValue("action") {
	case "retry":
		job, err = s.discovery.Retry(r.PathValue("id"))
	case "cancel":
		job, err = s.discovery.CancelJob(r.PathValue("id"))
	default:
		err = discovery.ErrInput
	}
	if err != nil {
		discoveryError(w, err)
		return
	}
	writeJSON(w, 202, job)
}
func (s *Server) cancelCreatorVideos(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) {
		return
	}
	var request struct {
		Session string `json:"session"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.discovery.CancelSession(request.Session, r.PathValue("id")); err != nil {
		discoveryError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "cancelled"})
}
