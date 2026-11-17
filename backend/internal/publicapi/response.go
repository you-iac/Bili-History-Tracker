// response.go 统一公共业务JSON与目标状态；HTTP错误仅用于请求协议和本服务故障。
// 业务失败必须保留ok=false，不能由HTTP200推断成功。见 docs/public-response.md。
package publicapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
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
	result := targetState{Kind: kind, ID: id, TrackingState: "absent", Status: "not_recorded"}
	t, exists, err := s.store.TrackingTarget(ctx, s.platform, kind, id)
	if err != nil {
		return result, err
	}
	i, err := s.reader.Content(ctx, s.platform, kind, id, time.Now())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Recorded = i.Recorded && (kind != "creator" || i.Seq > 0)
		result.Status = i.Status
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

func (s *Server) missingDetail(w http.ResponseWriter, r *http.Request, kind, id string) {
	target, err := s.targetStatus(r.Context(), kind, id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法读取目标状态。"})
		return
	}
	business(w, "DETAIL_NOT_RECORDED", "服务器暂无该目标的成功观测资料。", map[string]any{"target": target})
}
