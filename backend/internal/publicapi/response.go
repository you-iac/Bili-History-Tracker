// response.go 统一公共业务JSON与目标状态；HTTP错误仅用于请求协议和本服务故障。
// 业务失败必须保留ok=false，不能由HTTP200推断成功。见 docs/public-response.md。
package publicapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"bili-history-tracker/backend/internal/storage"
)

type targetState struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	TrackingState string `json:"tracking_state"`
	Tracking      bool   `json:"tracking"`
	Recorded      bool   `json:"recorded"`
	Status        string `json:"status"`
}

// targetStatus只读两类持久状态；读取失败不伪造“任务不存在”。
func (s *Server) targetStatus(ctx context.Context, kind, id string) (targetState, error) {
	return s.targetStatusWithContent(ctx, kind, id, nil)
}

// targetStatusWithContent 复用请求内的观测摘要，nil 表示尚未读取；不跨请求缓存。
// 任务存在/启停及运行状态仍实时读取，不能由摘要中较早的 Following 代替。
func (s *Server) targetStatusWithContent(ctx context.Context, kind, id string, cached *storage.ContentItem) (targetState, error) {
	result := targetState{Kind: kind, ID: id, TrackingState: "absent", Status: "not_recorded"}
	t, exists, err := s.store.TrackingTarget(ctx, s.platform, kind, id)
	if err != nil {
		return result, err
	}
	var i storage.ContentItem
	if cached != nil {
		i = *cached
	} else {
		i, err = s.reader.Content(ctx, s.platform, kind, id, time.Now())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	if i.Recorded {
		i.Following = exists && t.Enabled
		i.IntervalSeconds = t.IntervalSeconds
		result.Recorded = hasRecordedContent(i)
		result.Status = i.ObservationStatus(time.Now())
	}
	if exists {
		result.Tracking = t.Enabled
		result.TrackingState = "paused"
		if t.Enabled {
			result.TrackingState = "active"
			if !result.Recorded {
				result.Status = "pending"
			}
		} else {
			result.Status = "paused"
		}
	}
	if result.Tracking && s.tracker.IsRunning(kind, id) {
		result.Status = "running"
	}
	return result, nil
}

func business(w http.ResponseWriter, code, message string, extra map[string]any) {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["ok"] = false
	extra["code"] = code
	extra["error"] = message
	writeJSON(w, http.StatusOK, extra)
}

func (s *Server) missingDetail(ctx context.Context, w http.ResponseWriter, kind, id string, item storage.ContentItem) {
	target, err := s.targetStatusWithContent(ctx, kind, id, &item)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取目标状态。"})
		return
	}
	business(w, "DETAIL_NOT_RECORDED", "服务器暂无该目标的成功观测资料。", map[string]any{"target": target})
}
