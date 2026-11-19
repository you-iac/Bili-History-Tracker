// history.go 读取按目标限定的历史采样/版本及显式资料兜底，不携带平台Cookie。
// 不回退通用目录或旧完整详情；见 docs/local-first-metadata.md。
package remote

import (
	"context"
	"net/url"
	"strconv"
)

// BatchMetric 只含指定目标的历史点及显式请求的资料兜底。
type BatchMetric struct {
	Kind      string  `json:"kind"`
	ID        string  `json:"id"`
	Points    []Point `json:"points"`
	Truncated bool    `json:"truncated"`
	Fallback  *Item   `json:"fallback,omitempty"`
}

// MetricsBatch 请求一页7日采样，只接纳匹配ID且显式请求的资料兜底。
func (c *Client) MetricsBatch(ctx context.Context, base, kind string, ids, fallback []string) (map[string]BatchMetric, error) {
	var data struct {
		Items []BatchMetric `json:"items"`
	}
	input := struct {
		Kind     string   `json:"kind"`
		IDs      []string `json:"ids"`
		Days     int      `json:"days"`
		Fallback []string `json:"fallback_ids,omitempty"`
	}{kind, ids, 7, fallback}
	err := c.call(ctx, base, "POST", "/api/metrics/batch", input, &data)
	if err != nil {
		return nil, err
	}
	requested := map[string]bool{}
	allowedFallback := map[string]bool{}
	for _, id := range fallback {
		allowedFallback[id] = true
	}
	for _, id := range ids {
		requested[id] = true
	}
	result := map[string]BatchMetric{}
	bad := &Error{Message: "服务器批量历史响应不完整或标识不匹配。", Status: 502}
	for _, item := range data.Items {
		if item.Kind != kind || !requested[item.ID] || item.Points == nil {
			return nil, bad
		}
		if _, exists := result[item.ID]; exists {
			return nil, bad
		}
		for index, p := range item.Points {
			if p.At < 0 || p.Value < 0 || index > 0 && p.At <= item.Points[index-1].At {
				return nil, bad
			}
		}
		if item.Fallback != nil && (!allowedFallback[item.ID] || item.Fallback.Kind != kind || item.Fallback.ID != item.ID) {
			return nil, bad
		}
		result[item.ID] = item
	}
	if len(result) != len(requested) {
		return nil, bad
	}
	return result, nil
}

// Metadata 只用于本地失败后的已有资料读取，不触发远程采集。
func (c *Client) Metadata(ctx context.Context, base, kind, id string) (Item, error) {
	var data struct {
		Item Item `json:"item"`
	}
	err := c.call(ctx, base, "GET", "/api/metadata/"+url.PathEscape(kind)+"/"+url.PathEscape(id), nil, &data)
	if err == nil && (data.Item.Kind != kind || data.Item.ID != id) {
		err = &Error{Message: "服务器兜底资料身份不匹配。", Status: 502}
	}
	return data.Item, err
}

// History 只读取历史，指定seq时校验所选快照，不回退完整详情。
func (c *Client) History(ctx context.Context, base, kind, id string, page int, seq int64, days string) (Detail, error) {
	var data Detail
	if days == "" {
		days = "30"
	}
	if days != "7" && days != "30" && days != "90" && days != "all" || page < 1 || page > 100000 || seq < 0 {
		return data, &Error{Message: "历史范围、页码或版本无效。", Status: 400}
	}
	query := url.Values{"days": {days}, "page": {strconv.Itoa(page)}}
	if seq > 0 {
		query.Set("seq", strconv.FormatInt(seq, 10))
	}
	err := c.call(ctx, base, "GET", "/api/history/"+url.PathEscape(kind)+"/"+url.PathEscape(id)+"?"+query.Encode(), nil, &data)
	if err == nil && (data.Item.Kind != kind || data.Item.ID != id || seq > 0 && (data.SnapshotSeq != seq || len(data.Snapshot) == 0)) {
		err = &Error{Message: "服务器历史身份或版本不匹配。", Status: 502}
	}
	return data, err
}
