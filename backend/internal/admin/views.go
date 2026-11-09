// views.go 定义观察台页面视图、业务格式化和快照字段对比。
// 不把数据库表名或原始错误作为主要用户信息；展示口径见 docs/dashboard.md。
package admin

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

type pagination struct {
	Prev, Next         string
	Page, Pages, Total int
}
type change struct{ Name, Before, After string }
type logView struct {
	At                                              int64
	Kind, ID, Level, Action, Result, Detail, Status string
	Duration                                        int64
}
type viewData struct {
	CreatorID, Platform                                                                  string
	Nav, Title, Subtitle, Message, Error, Q, Sort, Following, Kind, Tab, Days, ReturnURL string
	Summary                                                                              storage.DashboardSummary
	Items, Videos, Creators, Issues                                                      []storage.ContentItem
	Item                                                                                 storage.ContentItem
	Page                                                                                 pagination
	RelatedPage                                                                          pagination
	Versions                                                                             storage.VersionPage
	VersionPage                                                                          pagination
	Snapshot                                                                             storage.Snapshot
	Payload, Description, SeriesJSON, CreatorRank, VideoRank, RelatedSort                string
	Series                                                                               storage.MetricSeries
	Lifetime                                                                             *int64
	LifetimeFrom, LifetimeTo                                                             int64
	Changes                                                                              []change
	Logs                                                                                 []logView
	LogSince                                                                             int64
	HasPending                                                                           bool
}

func integer(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case *int64:
		if v != nil {
			return *v, true
		}
	case nil:
		return 0, false
	}
	return 0, false
}
func compact(value any) string {
	n, ok := integer(value)
	if !ok {
		return "—"
	}
	if n >= 100000000 || n <= -100000000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e8), ".0") + "亿"
	}
	if n >= 10000 || n <= -10000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e4), ".0") + "万"
	}
	return strconv.FormatInt(n, 10)
}
func exact(value any) string {
	n, ok := integer(value)
	if !ok {
		return "未知"
	}
	s := strconv.FormatInt(n, 10)
	start := 0
	if strings.HasPrefix(s, "-") {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func signed(value *int64) string {
	if value == nil {
		return "样本不足"
	}
	if *value > 0 {
		return "+" + compact(value)
	}
	return compact(value)
}
func date(at int64) string {
	if at <= 0 {
		return "尚未记录"
	}
	return time.UnixMilli(at).In(beijing).Format("2006-01-02")
}
func clock(at int64) string {
	if at <= 0 {
		return "尚未采集"
	}
	return time.UnixMilli(at).In(beijing).Format("01-02 15:04")
}
func relative(at int64) string {
	if at <= 0 {
		return "等待首次采集"
	}
	d := time.Since(time.UnixMilli(at))
	if d < 0 {
		return clock(at)
	}
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	if d < 7*24*time.Hour {
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
	return date(at)
}
func statusLabel(status string) string {
	m := map[string]string{"ok": "采集正常", "unfollowed": "已记录", "profile_pending": "资料待采集", "pending": "等待采集", "running": "采集中", "error": "查询失败", "stale": "数据较旧", "recovered": "已恢复"}
	if v := m[status]; v != "" {
		return v
	}
	return "等待采集"
}
func reasonLabel(code string) string {
	m := map[string]string{"ok": "成功", "rate_limited": "请求受限流", "access_restricted": "访问受限", "not_found": "上游未找到", "upstream_failure": "上游请求失败", "query_failed": "查询未完成", "invalid_response": "上游数据无效", "invalid_payload": "快照解析失败", "store_failed": "本地保存失败", "invalid_input": "标识格式无效", "unsupported_operation": "接口暂不支持"}
	if s := m[code]; s != "" {
		return s
	}
	return "尚未查询"
}
func detailURL(kind, id string) string { return "/detail/" + kind + "/" + url.PathEscape(id) }
func sourceURL(kind, id string) string {
	if kind == "creator" {
		return "https://space.bilibili.com/" + url.PathEscape(id)
	}
	return "https://www.bilibili.com/video/" + url.PathEscape(id)
}
func templateFunctions() template.FuncMap {
	return template.FuncMap{
		"compact": compact, "exact": exact, "signed": signed, "date": date, "clock": clock, "relative": relative, "statusLabel": statusLabel, "reason": reasonLabel, "detailURL": detailURL, "sourceURL": sourceURL,
		"kindLabel": func(kind string) string {
			if kind == "creator" {
				return "UP 主"
			}
			return "视频"
		},
		"metricLabel": func(kind string) string {
			if kind == "creator" {
				return "粉丝"
			}
			return "播放"
		},
		"initial": func(name string) string {
			r := []rune(strings.TrimSpace(name))
			if len(r) == 0 {
				return "B"
			}
			return string(r[0])
		},
		"interval": formatInterval, "hours": func(seconds int64) string { return strconv.FormatFloat(float64(seconds)/3600, 'f', 1, 64) },
		"eq": func(a, b string) bool { return a == b }, "utcDate": func(at int64) string { return time.UnixMilli(at).UTC().Format("2006-01-02") },
		"icon":  icon,
		"eqInt": func(a, b int64) bool { return a == b }, "addOne": func(i int) int { return i + 1 },
		"tone": func(v *int64) string {
			if v == nil || *v == 0 {
				return "neutral"
			}
			if *v > 0 {
				return "positive"
			}
			return "negative"
		},
		"actionLabel": func(action string) string {
			labels := map[string]string{"query_video": "视频查询", "query_creator": "UP 主资料", "discover": "投稿发现", "metric": "指标采样", "schedule": "自动调度", "status": "状态保存"}
			if value := labels[action]; value != "" {
				return value
			}
			return "运行记录"
		},
	}
}

// icon 只返回代码中固定的 SVG 路径，不接受上游内容或用户输入作为 HTML。
func icon(name string) template.HTML {
	paths := map[string]string{
		"overview": `<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`,
		"videos":   `<rect x="3" y="5" width="18" height="14" rx="3"/><path d="m10 9 5 3-5 3z"/>`,
		"creators": `<circle cx="10" cy="8" r="3"/><path d="M4 20v-2a6 6 0 0 1 12 0v2M17 5a3 3 0 0 1 0 6m2 9v-2a6 6 0 0 0-2-4"/>`,
		"activity": `<path d="M3 12h4l3-8 4 16 3-8h4"/>`,
		"manage":   `<path d="M12 3v18M3 12h18"/><circle cx="12" cy="12" r="9"/>`,
		"logs":     `<path d="M8 3h8l4 4v14H4V3zM8 11h8m-8 4h8m-8 4h4"/>`,
		"search":   `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>`,
		"arrow":    `<path d="M5 12h14m-5-5 5 5-5 5"/>`,
		"chevron":  `<path d="m9 5 7 7-7 7"/>`,
		"refresh":  `<path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-2l2 3M4 16l2 3a7 7 0 0 0 12-2"/>`,
		"trend":    `<path d="m3 17 6-6 4 4 8-10m-6 0h6v6"/>`,
		"alert":    `<path d="m12 3 10 18H2zM12 9v5m0 3h.01"/>`,
		"check":    `<path d="m5 12 4 4L19 6"/>`,
		"external": `<path d="M14 3h7v7m0-7-11 11M9 3H3v18h18v-6"/>`,
		"menu":     `<path d="M4 6h16M4 12h16M4 18h16"/>`,
		"close":    `<path d="m6 6 12 12M18 6 6 18"/>`,
	}
	return template.HTML(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + paths[name] + `</svg>`)
}

func seriesJSON(series storage.MetricSeries) string {
	raw, _ := json.Marshal(series)
	return string(raw)
}
func makePager(rurl *url.URL, page, limit, total int, param string) pagination {
	p := pagination{Page: page, Pages: max(1, (total+limit-1)/limit), Total: total}
	query := rurl.Query()
	query.Del("message")
	query.Del("error")
	if page > 1 {
		query.Set(param, strconv.Itoa(page-1))
		p.Prev = rurl.Path + "?" + query.Encode()
	}
	if page < p.Pages {
		query.Set(param, strconv.Itoa(page+1))
		p.Next = rurl.Path + "?" + query.Encode()
	}
	return p
}

// contentChanges 仅对照现有指纹投影的内容字段，排除计数与合集数据。
func contentChanges(kind string, before, after []byte) []change {
	decode := func(raw []byte) map[string]any {
		var m map[string]any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		_ = d.Decode(&m)
		return m
	}
	a, b := decode(before), decode(after)
	get := func(m map[string]any, path string) any {
		var value any = m
		for _, key := range strings.Split(path, ".") {
			obj, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			value = obj[key]
		}
		return value
	}
	fields := [][2]string{{"title", "标题"}, {"desc", "简介"}, {"pic", "封面"}, {"pubdate", "发布时间"}, {"duration", "时长"}, {"owner.mid", "作者归属"}, {"dynamic", "动态摘要"}, {"pages", "分 P"}}
	if kind == "creator" {
		fields = [][2]string{{"card.name", "昵称"}, {"card.sign", "个人签名"}, {"card.face", "头像"}, {"card.mid", "作者标识"}}
	}
	normalize := func(value any, path string) string {
		if path == "pages" {
			var projected []map[string]any
			if pages, ok := value.([]any); ok {
				for _, p := range pages {
					if m, ok := p.(map[string]any); ok {
						projected = append(projected, map[string]any{"page": m["page"], "cid": m["cid"], "part": m["part"], "duration": m["duration"]})
					}
				}
			}
			numeric := func(v any) int64 { n, _ := strconv.ParseInt(fmt.Sprint(v), 10, 64); return n }
			sort.Slice(projected, func(i, j int) bool {
				if numeric(projected[i]["page"]) != numeric(projected[j]["page"]) {
					return numeric(projected[i]["page"]) < numeric(projected[j]["page"])
				}
				return numeric(projected[i]["cid"]) < numeric(projected[j]["cid"])
			})
			value = projected
		}
		if text, ok := value.(string); ok {
			if path == "pic" || path == "card.face" {
				text = strings.SplitN(strings.SplitN(text, "?", 2)[0], "@", 2)[0]
			}
			return text
		}
		if value == nil {
			return "未提供"
		}
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	result := []change{}
	for _, field := range fields {
		left, right := normalize(get(a, field[0]), field[0]), normalize(get(b, field[0]), field[0])
		if left != right {
			result = append(result, change{Name: field[1], Before: left, After: right})
		}
	}
	return result
}
