// image.go 从既有内容快照读取图片URL，仅允许B站图片CDN；不下载或代理图片。
// HTTP地址升级为HTTPS，禁止凭据、端口及其他协议。见 docs/client-launch-media.md。
package remote

import (
	"encoding/json"
	"net/url"
	"strings"
)

func NormalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	if len(raw) > 4096 || strings.ContainsAny(raw, "\\\r\n\t") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Port() != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "hdslb.com" && !strings.HasSuffix(host, ".hdslb.com") && host != "biliimg.com" && !strings.HasSuffix(host, ".biliimg.com") {
		return ""
	}
	u.Scheme, u.Host, u.Fragment = "https", host, ""
	return u.String()
}

func SnapshotImageURL(kind string, raw json.RawMessage) string {
	var snapshot struct {
		Pic  string `json:"pic"`
		Card struct {
			Face string `json:"face"`
		} `json:"card"`
	}
	if json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	if kind == "creator" {
		return NormalizeImageURL(snapshot.Card.Face)
	}
	if kind == "video" {
		return NormalizeImageURL(snapshot.Pic)
	}
	return ""
}
