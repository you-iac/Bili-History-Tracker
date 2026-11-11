// Package bilibili 提供本地 Cookie 登录验证与 UP 主投稿分页。
// Cookie 显式传入，不读取环境变量、不回退公开合集发现、不返回原始响应。
// 固定 HTTPS 地址且禁止重定向；设计与接口来源见 docs/client-discovery.md。
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const PageSize = 30

var bvidPattern = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

// Error 仅包含可向界面展示的错误，不含 Cookie、请求 URL 或响应正文。
type Error struct {
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Message }

// Account 是验证后的账号摘要，不包含平台凭据。
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Video 只保留标识、标题和发布时间；不推断服务器跟踪状态。
type Video struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	PublishedAt int64  `json:"published_at"`
}
type Page struct {
	Videos []Video
	Total  int
}

// Client 可并发复用，每次请求独立传入 Cookie，防止账号串用。
type Client struct{ http *http.Client }

func New(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copy}
}

// NormalizeCookie 接受 Cookie 请求头的值和可选前缀，拒绝多行与重复名称。
func NormalizeCookie(value string) (string, error) {
	invalid := &Error{Code: "invalid_cookie", Message: "请粘贴有效的 Cookie 请求头，且包含 SESSDATA。"}
	if len(value) > 32<<10 || strings.ContainsAny(value, "\r\n\x00") {
		return "", invalid
	}
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.EqualFold(value[:7], "cookie:") {
		value = strings.TrimSpace(value[7:])
	}
	var parts []string
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, val, ok := strings.Cut(item, "=")
		if !ok || (&http.Cookie{Name: name, Value: val}).Valid() != nil || (name == "SESSDATA" && val == "") || seen[name] {
			return "", invalid
		}
		seen[name] = true
		parts = append(parts, name+"="+val)
	}
	if !seen["SESSDATA"] {
		return "", invalid
	}
	return strings.Join(parts, "; "), nil
}

// CreatorID 仅解析正整数 UID 或 B站个人空间链接，不访问输入中的 URL。
func CreatorID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "https://") || strings.HasPrefix(input, "http://") {
		u, err := url.Parse(input)
		if err != nil || u.User != nil || u.Host != "space.bilibili.com" {
			return "", &Error{Code: "invalid_uid", Message: "请输入有效的 UP 主 UID 或 B站个人空间链接。"}
		}
		input = strings.Trim(u.Path, "/")
	}
	uid, err := strconv.ParseInt(input, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != input {
		return "", &Error{Code: "invalid_uid", Message: "UID 必须是正整数，例如 122541。"}
	}
	return input, nil
}

// Login 同时检查 nav 业务码和 isLogin，匿名响应也可能携带 WBI 材料。
func (c *Client) Login(ctx context.Context, cookie string) (Account, Keys, error) {
	var data struct {
		IsLogin bool   `json:"isLogin"`
		MID     int64  `json:"mid"`
		Name    string `json:"uname"`
		WBI     struct {
			Image string `json:"img_url"`
			Sub   string `json:"sub_url"`
		} `json:"wbi_img"`
	}
	if err := c.get(ctx, "/x/web-interface/nav", nil, cookie, "", &data); err != nil {
		return Account{}, Keys{}, err
	}
	if !data.IsLogin || data.MID <= 0 || data.Name == "" {
		return Account{}, Keys{}, &Error{Code: "login_required", Message: "Cookie 已失效，请在 B站重新登录后更新。"}
	}
	keys, err := NewKeys(data.WBI.Image, data.WBI.Sub)
	if err != nil {
		return Account{}, Keys{}, err
	}
	return Account{ID: strconv.FormatInt(data.MID, 10), Name: data.Name}, keys, nil
}

// Videos 无总条数上限。合作视频的 mid 可能不同于 UP，不能按归属过滤。
func (c *Client) Videos(ctx context.Context, cookie, uid string, page int, keys Keys) (Page, error) {
	if normalized, err := CreatorID(uid); err != nil || normalized != uid || page < 1 {
		return Page{}, &Error{Code: "invalid_uid", Message: "投稿分页参数无效。"}
	}
	query := url.Values{"mid": {uid}, "pn": {strconv.Itoa(page)}, "ps": {strconv.Itoa(PageSize)}, "order": {"pubdate"}, "tid": {"0"}, "platform": {"web"}, "web_location": {"1550101"}}
	query = keys.Sign(query, time.Now())
	var data struct {
		Voucher string `json:"v_voucher"`
		Risk    bool   `json:"is_risk"`
		List    *struct {
			Videos []struct {
				ID      string `json:"bvid"`
				Title   string `json:"title"`
				Created int64  `json:"created"`
			} `json:"vlist"`
		} `json:"list"`
		Page *struct {
			Count *int `json:"count"`
			PN    int  `json:"pn"`
			PS    int  `json:"ps"`
		} `json:"page"`
	}
	if err := c.get(ctx, "/x/space/wbi/arc/search", query, cookie, uid, &data); err != nil {
		return Page{}, err
	}
	if data.Voucher != "" || data.Risk {
		return Page{}, &Error{Code: "restricted", Message: "B站要求额外验证，请在 B站完成验证后重试。"}
	}
	invalid := &Error{Code: "invalid_response", Message: "B站返回的投稿分页数据不完整，已保留之前的结果。"}
	if data.List == nil || data.Page == nil || data.Page.Count == nil || *data.Page.Count < 0 || data.Page.PN != page || data.Page.PS != PageSize || len(data.List.Videos) > PageSize {
		return Page{}, invalid
	}
	result := Page{Total: *data.Page.Count, Videos: make([]Video, 0, len(data.List.Videos))}
	for _, v := range data.List.Videos {
		if !bvidPattern.MatchString(v.ID) || v.Created < 0 {
			return Page{}, invalid
		}
		result.Videos = append(result.Videos, Video{ID: v.ID, Title: v.Title, PublishedAt: v.Created})
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, route string, query url.Values, cookie, uid string, target any) error {
	endpoint := "https://api.bilibili.com" + route
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &Error{Code: "network", Message: "无法创建平台请求。"}
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://space.bilibili.com/"+uid)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: "network", Message: "连接 B站失败或超时，请检查网络后重试。"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		wait := 3 * time.Second
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
			wait = min(time.Duration(n)*time.Second, 30*time.Second)
		}
		return &Error{Code: "rate_limited", Message: "B站请求过于频繁，稍后可继续扫描。", RetryAfter: wait}
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 412 {
		return &Error{Code: "restricted", Message: "B站暂时限制访问，请在 B站完成验证后重试。"}
	}
	if resp.StatusCode != 200 {
		return &Error{Code: "upstream", Message: "B站服务暂时不可用，请稍后继续扫描。"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	invalid := &Error{Code: "invalid_response", Message: "B站返回了无法解析的响应，请稍后重试。"}
	if err != nil || len(body) > 4<<20 {
		return invalid
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil {
		return invalid
	}
	if *envelope.Code != 0 {
		switch *envelope.Code {
		case -101, -102, -111:
			return &Error{Code: "login_required", Message: "Cookie 已失效或账号不可用，请重新登录 B站并更新。"}
		case -352, -412, -403:
			return &Error{Code: "restricted", Message: fmt.Sprintf("B站要求验证或限制访问（%d），请在 B站验证后继续。", *envelope.Code)}
		case -799, -509:
			return &Error{Code: "rate_limited", Message: "B站请求过于频繁，稍后可继续扫描。", RetryAfter: 3 * time.Second}
		default:
			return &Error{Code: "upstream", Message: fmt.Sprintf("B站请求未成功（%d），已保留结果，可稍后继续。", *envelope.Code)}
		}
	}
	if string(envelope.Data) == "null" || len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, target) != nil {
		return invalid
	}
	return nil
}
