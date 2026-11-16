// input.go 约束公共 JSON 与 B站标识；只解析标准链接，不访问客户端传入的 URL。
package publicapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func sameOriginJSON(w http.ResponseWriter, r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeJSON(w, 415, map[string]string{"error": "请使用 JSON 请求。"})
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSON(w, 403, map[string]string{"error": "不接受浏览器跨站提交。"})
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
	err := decoder.Decode(value)
	if err == nil {
		err = decoder.Decode(new(any))
		if err == io.EOF {
			return true
		}
	}
	var sizeError *http.MaxBytesError
	code := 400
	if errors.As(err, &sizeError) {
		code = 413
	}
	writeJSON(w, code, map[string]string{"error": "JSON 请求格式无效或超过 64 KiB。"})
	return false
}

var bvidInput = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

func validID(kind, id string) bool {
	if kind == "creator" {
		value, err := strconv.ParseInt(id, 10, 64)
		return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
	}
	return kind == "video" && bvidInput.MatchString(id)
}

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
