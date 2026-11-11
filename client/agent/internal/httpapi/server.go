// Package httpapi 提供仅监听回环地址的客户端网页、会话、Cookie 配置与扫描 API。
// Host/Origin、HttpOnly 会话和写操作 CSRF 分别检查；首次连接用启动链接的片段密钥换取会话。
// 不向网页返回 Cookie，不提供任意 URL 代理或本地文件读取。见 docs/client-discovery.md。
package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/scanner"
	"bili-history-tracker/client/frontend"
)

type session struct {
	csrf    string
	expires time.Time
}
type Server struct {
	service   *scanner.Service
	address   string
	bootstrap string
	mu        sync.Mutex
	sessions  map[string]session
	mux       *http.ServeMux
}

// New 的 address 必须是实际监听的回环 host:port，不能来自请求头。
func New(service *scanner.Service, address string) (*Server, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	s := &Server{service: service, address: address, bootstrap: token, sessions: map[string]session{}, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/session", s.connect)
	s.mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		current, _ := s.session(r)
		writeJSON(w, 200, map[string]string{"csrf_token": current.csrf})
	})
	s.mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.service.Config()) })
	s.mux.HandleFunc("PUT /api/config/cookie", s.saveCookie)
	s.mux.HandleFunc("POST /api/config/validate", func(w http.ResponseWriter, r *http.Request) {
		a, err := s.service.Validate(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, a)
	})
	s.mux.HandleFunc("DELETE /api/config/cookie", func(w http.ResponseWriter, r *http.Request) {
		if err := s.service.ClearCookie(); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, s.service.Config())
	})
	s.mux.HandleFunc("GET /api/scan", s.result)
	s.mux.HandleFunc("POST /api/scan", s.start)
	s.mux.HandleFunc("POST /api/scan/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := s.service.Cancel(); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, s.service.Result(0, 0))
	})
	s.mux.HandleFunc("GET /api/scan/export", s.export)
	static := http.FileServerFS(frontend.Files)
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
	return s, nil
}

// BootstrapToken 仅用于构造启动终端的连接链接，不是平台凭据，不放入查询串或请求日志。
func (s *Server) BootstrapToken() string { return s.bootstrap }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.Host != s.address {
		apiError(w, 403, "invalid_host", "不允许此访问地址。")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://"+s.address {
			apiError(w, 403, "invalid_origin", "不允许此来源访问本地 API。")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
			apiError(w, 403, "invalid_origin", "请从 Agent 的本地页面访问。")
			return
		}
		unsafe := r.Method != "GET" && r.Method != "HEAD"
		if unsafe {
			contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if origin != "http://"+s.address || err != nil || contentType != "application/json" {
				apiError(w, 403, "invalid_origin", "写操作需要同源 JSON 请求。")
				return
			}
		}
		if r.URL.Path != "/api/session" || r.Method != "POST" {
			current, ok := s.session(r)
			if !ok {
				apiError(w, 401, "agent_unauthorized", "请使用 Agent 启动时显示的完整链接连接。")
				return
			}
			if unsafe && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(current.csrf)) != 1 {
				apiError(w, 403, "invalid_csrf", "页面会话已更新，请刷新后重试。")
				return
			}
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) session(r *http.Request) (session, bool) {
	cookie, err := r.Cookie("bht_client_session")
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[cookie.Value]
	if ok && current.expires.Before(time.Now()) {
		delete(s.sessions, cookie.Value)
		ok = false
	}
	return current, ok
}
func randomToken() (string, error) {
	data := make([]byte, 32)
	_, err := rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data), err
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &input) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Token), []byte(s.bootstrap)) != 1 {
		apiError(w, 401, "agent_unauthorized", "连接密钥不正确，请使用启动终端中的完整链接。")
		return
	}
	id, err := randomToken()
	if err != nil {
		writeError(w, err)
		return
	}
	csrf, err := randomToken()
	if err != nil {
		writeError(w, err)
		return
	}
	s.mu.Lock()
	for key, value := range s.sessions {
		if value.expires.Before(time.Now()) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 32 {
		s.mu.Unlock()
		apiError(w, 429, "session_limit", "连接会话过多，请关闭多余页面并重启 Agent。")
		return
	}
	s.sessions[id] = session{csrf: csrf, expires: time.Now().Add(12 * time.Hour)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "bht_client_session", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	writeJSON(w, 200, map[string]string{"csrf_token": csrf})
}
func (s *Server) saveCookie(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Cookie string `json:"cookie"`
	}
	if !decode(w, r, &input) {
		return
	}
	a, err := s.service.Configure(r.Context(), input.Cookie)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, a)
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CreatorID string `json:"creator_id"`
		Resume    bool   `json:"resume"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := s.service.Start(input.CreatorID, input.Resume); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 202, s.service.Result(0, 0))
}
func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	offset, limit := 0, 50
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			apiError(w, 400, "invalid_input", "分页位置无效。")
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			apiError(w, 400, "invalid_input", "每页数量必须在 1 到 100 之间。")
			return
		}
	}
	writeJSON(w, 200, s.service.Result(offset, limit))
}
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "txt"
	}
	if format != "txt" && format != "json" {
		apiError(w, 400, "invalid_input", "仅支持 TXT 或 JSON 导出。")
		return
	}
	result := s.service.Result(0, math.MaxInt)
	if result.Job == nil {
		apiError(w, 404, "not_found", "还没有扫描结果。")
		return
	}
	state := "partial"
	if result.Job.State == "complete" {
		state = "complete"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="up-%s-%s.%s"`, result.Job.CreatorID, state, format))
	if format == "json" {
		writeJSON(w, 200, result)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, video := range result.Videos {
		fmt.Fprintln(w, video.ID)
	}
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		apiError(w, 400, "invalid_input", "请求内容无效或过长。")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		apiError(w, 400, "invalid_input", "请求必须只包含一个 JSON 对象。")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, err error) {
	var known *bilibili.Error
	if errors.As(err, &known) {
		status := 502
		switch known.Code {
		case "invalid_cookie", "invalid_uid", "invalid_input":
			status = 400
		case "login_required":
			status = 422
		case "conflict":
			status = 409
		case "storage":
			status = 500
		}
		apiError(w, status, known.Code, known.Message)
		return
	}
	apiError(w, 500, "internal", "操作未完成，请稍后重试。")
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
