// detail_sources.go 只调用独立历史、保存资料及版本接口，不自动回退重型旧详情。
// 所有服务器资料保留身份和 seq 绑定。见 docs/client-detail-data-sources.md。
package remote

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// Record 表示服务器当前版本指针，独立于平台当前资料。
type Record struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	ObservedAt int64  `json:"observed_at"`
	Recorded   bool   `json:"recorded"`
}
type Series struct {
	Points    []Point `json:"points"`
	Truncated bool    `json:"truncated"`
}
type History struct {
	Record  Record `json:"record"`
	Target  Status `json:"target"`
	Metrics Series `json:"metrics"`
}
type Saved struct {
	Item        Item   `json:"item"`
	Description string `json:"description"`
	SnapshotAt  int64  `json:"snapshot_at"`
}
type VersionCursor struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	Items      []Version `json:"items"`
	HasMore    bool      `json:"has_more"`
	NextBefore int64     `json:"next_before"`
}
type VersionSnapshot struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Seq      int64           `json:"seq"`
	At       int64           `json:"at"`
	Snapshot json.RawMessage `json:"snapshot"`
}

func detailPath(kind, id string) string {
	return "/api/detail/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}
func invalidDetail() error {
	return &Error{Code: "INVALID_RESPONSE", Status: 502, Message: "服务器详情数据身份或内容无效。"}
}
func ValidDays(days string) bool { return days == "7" || days == "30" || days == "90" || days == "all" }

// History 校验目标、记录存在性和真实采样；旧服务不支持时返回错误，不降级旧详情。
func (c *Client) History(ctx context.Context, base, kind, id, days string) (History, error) {
	var r History
	if !ValidDays(days) {
		return r, &Error{Status: 400, Message: "时间范围无效。"}
	}
	err := c.call(ctx, base, "GET", detailPath(kind, id)+"?days="+url.QueryEscape(days), nil, &r)
	if err != nil {
		return r, err
	}
	if r.Record.Kind != kind || r.Record.ID != id || r.Record.Seq < 0 || r.Record.ObservedAt < 0 || r.Record.Recorded != (r.Record.Seq > 0) || r.Target.Recorded != r.Record.Recorded || r.Metrics.Points == nil {
		return r, invalidDetail()
	}
	if err = r.Target.normalize(kind, id); err != nil {
		return r, err
	}
	if len(r.Metrics.Points) > 5000 {
		return r, invalidDetail()
	}
	var last int64
	for _, p := range r.Metrics.Points {
		if p.At < last || p.At < 0 || p.Value < 0 {
			return r, invalidDetail()
		}
		last = p.At
	}
	return r, nil
}

// Saved 只读取服务器最后保存资料，响应的 seq 与资料同次绑定。
func (c *Client) Saved(ctx context.Context, base, kind, id string) (Saved, error) {
	var r Saved
	err := c.call(ctx, base, "GET", detailPath(kind, id)+"/saved", nil, &r)
	if err == nil && (r.Item.Kind != kind || r.Item.ID != id || r.Item.Seq < 1 || !r.Item.Recorded || r.Item.ObservedAt < 0 || r.SnapshotAt < 0 || r.Item.Metric != nil && *r.Item.Metric < 0) {
		err = invalidDetail()
	}
	return r, err
}

// Versions 使用递减 seq 游标，拒绝重复、倒序错误和不一致的下一页指针。
func (c *Client) Versions(ctx context.Context, base, kind, id string, before int64) (VersionCursor, error) {
	var r VersionCursor
	if before < 0 {
		return r, &Error{Status: 400, Message: "版本游标无效。"}
	}
	path := detailPath(kind, id) + "/versions"
	if before > 0 {
		path += "?before=" + strconv.FormatInt(before, 10)
	}
	err := c.call(ctx, base, "GET", path, nil, &r)
	if err != nil {
		return r, err
	}
	if r.Kind != kind || r.ID != id || r.Items == nil || len(r.Items) > 10 {
		return r, invalidDetail()
	}
	last := before
	for _, v := range r.Items {
		if v.Seq < 1 || v.At < 0 || last > 0 && v.Seq >= last {
			return r, invalidDetail()
		}
		last = v.Seq
	}
	if r.HasMore && (len(r.Items) != 10 || r.NextBefore != last) || !r.HasMore && r.NextBefore != 0 {
		return r, invalidDetail()
	}
	return r, nil
}

// Snapshot 只接受明确版本，不夹带曲线或当前资料读取。
func (c *Client) Snapshot(ctx context.Context, base, kind, id string, seq int64) (VersionSnapshot, error) {
	var r VersionSnapshot
	if seq < 1 {
		return r, &Error{Status: 400, Message: "版本号无效。"}
	}
	err := c.call(ctx, base, "GET", detailPath(kind, id)+"/snapshot?seq="+strconv.FormatInt(seq, 10), nil, &r)
	if err == nil && (r.Kind != kind || r.ID != id || r.Seq != seq || len(r.Snapshot) == 0 || string(r.Snapshot) == "null") {
		err = invalidDetail()
	}
	return r, err
}
