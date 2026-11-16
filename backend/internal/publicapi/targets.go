// targets.go 提供兼容单目标的有界批量申请；成功才确认跟踪，超出前 N 项不执行。
// 整批共用超时并顺序处理，不能通过一个 HTTP 请求放大无限上游并发。见 docs/public-api.md。
package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"bili-history-tracker/backend/internal/tracker"
)

type targetInput struct {
	Kind string `json:"type"`
	ID   string `json:"id"`
}

type targetResult struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	HTTPStatus int    `json:"http_status"`
	Tracking   bool   `json:"tracking"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) requestTarget(w http.ResponseWriter, r *http.Request) {
	if !sameOriginJSON(w, r) {
		return
	}
	var input struct {
		Kind    json.RawMessage `json:"type"`
		ID      json.RawMessage `json:"id"`
		Targets json.RawMessage `json:"targets"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if input.Targets == nil {
		var target targetInput
		if json.Unmarshal(input.Kind, &target.Kind) != nil || json.Unmarshal(input.ID, &target.ID) != nil {
			writeJSON(w, 400, map[string]string{"error": "单目标 type 和 id 必须为字符串。"})
			return
		}
		result := s.submit(ctx, target)
		if result.Error != "" {
			if result.HTTPStatus == 429 {
				w.Header().Set("Retry-After", "1")
			}
			writeJSON(w, result.HTTPStatus, map[string]string{"error": result.Error})
			return
		}
		writeJSON(w, 200, map[string]any{"kind": result.Kind, "id": result.ID, "tracking": true, "status": result.Status})
		return
	}
	var targets []json.RawMessage
	if input.Kind != nil || input.ID != nil || json.Unmarshal(input.Targets, &targets) != nil || len(targets) == 0 {
		writeJSON(w, 400, map[string]string{"error": "批量 targets 不能为空，也不能混用单目标字段。"})
		return
	}
	results := s.submitBatch(ctx, targets)
	writeJSON(w, 200, map[string]any{"received": len(targets), "processed": len(results), "ignored": len(targets) - len(results), "results": results})
}

func (s *Server) submitBatch(ctx context.Context, inputs []json.RawMessage) []targetResult {
	n := min(len(inputs), s.config.BatchMax)
	results := make([]targetResult, 0, n)
	for index, raw := range inputs[:n] {
		var input targetInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&input)
		if err == nil && decoder.Decode(new(any)) != io.EOF {
			err = errors.New("extra JSON value")
		}
		result := targetResult{Kind: input.Kind, ID: input.ID, HTTPStatus: 400, Status: "error", Error: "目标 JSON 格式无效。"}
		if err == nil {
			result = s.submit(ctx, input)
		}
		result.Index = index
		results = append(results, result)
	}
	return results
}

func (s *Server) submit(ctx context.Context, input targetInput) targetResult {
	input.ID = normalizeID(input.Kind, input.ID)
	result := targetResult{Kind: input.Kind, ID: input.ID, HTTPStatus: 400, Status: "error", Error: "目标标识无效。"}
	if !validID(input.Kind, input.ID) {
		return result
	}
	if ctx.Err() != nil {
		result.HTTPStatus, result.Error = 504, "本次处理已超时，请查询状态后重试。"
		return result
	}
	state, err := s.tracker.RequestTarget(ctx, input.Kind, input.ID)
	if err == nil {
		result.HTTPStatus, result.Tracking, result.Status, result.Error = 200, true, state, ""
		return result
	}
	result.HTTPStatus, result.Error = 502, "服务器无法验证或保存该目标，尚未确认加入跟踪。"
	switch {
	case errors.Is(err, tracker.ErrTargetBusy):
		result.HTTPStatus, result.Error = 409, "目标正在采集中，请先刷新状态或稍后重试。"
	case errors.Is(err, tracker.ErrRequestCapacity):
		result.HTTPStatus, result.Error = 429, "服务器采集任务较多，请稍后重试。"
	case errors.Is(err, tracker.ErrTargetPaused):
		result.HTTPStatus, result.Error = 409, "目标已被服务器暂停，请联系管理员。"
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		result.HTTPStatus, result.Error = 504, "本次处理已超时，请查询状态后重试。"
	}
	return result
}
