// targets.go 提供单目标和有界批量申请。最后一次读取确认任务已启用才返回成功。
// 整批共用接纳时限，已开始保存的项目独立有界收尾。阶段和结果语义见 docs/request-stages.md。
package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"bili-history-tracker/backend/internal/tracker"
)

type targetInput struct {
	Kind string `json:"type"`
	ID   string `json:"id"`
}

type targetResult struct {
	Stage      string       `json:"stage"`
	Outcome    string       `json:"outcome"`
	OK         bool         `json:"ok"`
	Code       string       `json:"code"`
	Result     string       `json:"result,omitempty"`
	Target     *targetState `json:"target,omitempty"`
	RetryAfter int          `json:"retry_after_seconds,omitempty"`
	Index      int          `json:"index"`
	Kind       string       `json:"kind"`
	ID         string       `json:"id"`
	HTTPStatus int          `json:"http_status"`
	Tracking   bool         `json:"tracking"`
	Status     string       `json:"status"`
	Error      string       `json:"error,omitempty"`
}

// 无最终状态时省略兼容 tracking 字段，避免把默认 false 当成已查明任务不存在。
func (r targetResult) MarshalJSON() ([]byte, error) {
	type fields targetResult
	if r.Target == nil {
		return json.Marshal(struct {
			fields
			Tracking *bool `json:"tracking,omitempty"`
		}{fields: fields(r)})
	}
	return json.Marshal(fields(r))
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
	ctx, cancel := context.WithTimeout(r.Context(), s.admissionTimeout)
	defer cancel()
	if input.Targets == nil {
		var target targetInput
		if json.Unmarshal(input.Kind, &target.Kind) != nil || json.Unmarshal(input.ID, &target.ID) != nil {
			writeJSON(w, 400, map[string]string{"error": "单目标 type 和 id 必须为字符串。"})
			return
		}
		result := s.submit(ctx, target)
		if !result.OK {
			if result.HTTPStatus == 429 {
				w.Header().Set("Retry-After", "1")
			}
			status := 200
			if result.HTTPStatus == 400 || result.HTTPStatus == 500 {
				status = result.HTTPStatus
			}
			writeJSON(w, status, result)
			return
		}
		writeJSON(w, result.HTTPStatus, map[string]any{
			"stage":          result.Stage,
			"outcome":        result.Outcome,
			"ok":             result.OK,
			"code":           result.Code,
			"kind":           result.Kind,
			"id":             result.ID,
			"tracking":       result.Tracking,
			"tracking_state": result.Target.TrackingState,
			"recorded":       result.Target.Recorded,
			"status":         result.Status,
			"result":         result.Result,
		})
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
		result := targetResult{Stage: "check", Outcome: "not_applied", Code: "INVALID_TARGET", Kind: input.Kind, ID: input.ID, HTTPStatus: 400, Status: "error", Error: "目标 JSON 格式无效。"}
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
	result := targetResult{Stage: "check", Outcome: "not_applied", Code: "INVALID_TARGET", Kind: input.Kind, ID: input.ID, HTTPStatus: 400, Status: "error", Error: "目标标识无效。"}
	if !validID(input.Kind, input.ID) {
		return result
	}
	if ctx.Err() != nil {
		result.Code = "REQUEST_TIMEOUT"
		result.HTTPStatus, result.Error = 504, "本次处理已超时，请查询状态后重试。"
		return result
	}
	state, err := s.tracker.RequestTarget(ctx, input.Kind, input.ID)
	if err == nil || errors.Is(err, tracker.ErrTargetPaused) {
		// 管理操作可能发生在接纳申请和状态读取之间，两处应答共用这次确认结果。
		target, stateErr := s.confirmTarget(ctx, input.Kind, input.ID)
		if stateErr != nil {
			return requestFailure(result, &tracker.RequestError{Stage: "confirm", Outcome: "unknown", Cause: errors.Join(tracker.ErrTargetStorage, stateErr)})
		}
		result.Stage, result.Outcome = "confirm", "confirmed"
		result.Target = &target
		result.Tracking, result.Status = target.Tracking, target.Status
		switch target.TrackingState {
		case "active":
			result.OK, result.Code, result.HTTPStatus, result.Error = true, "OK", http.StatusOK, ""
			result.Result = "existing"
			if state == "tracking" {
				result.Result = "created"
			}
		case "paused":
			result.HTTPStatus, result.Code, result.Error = http.StatusConflict, "TARGET_PAUSED", "目标已被服务器暂停，请联系管理员。"
		default:
			result.HTTPStatus, result.Code, result.Error = http.StatusBadGateway, "TARGET_SUBMISSION_FAILED", "提交后目标已不在跟踪列表，请查询状态后重试。"
		}
		return result
	}
	result = requestFailure(result, err)
	// 不额外启动第二轮确认预算；原阶段失败不暗示任务不存在，后续客户端先查状态。
	return result
}

func (s *Server) confirmTarget(ctx context.Context, kind, id string) (targetState, error) {
	phase, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.confirmTimeout)
	defer cancel()
	return s.targetStatus(phase, kind, id)
}

func requestFailure(result targetResult, err error) targetResult {
	var phase *tracker.RequestError
	if errors.As(err, &phase) {
		result.Stage, result.Outcome = phase.Stage, phase.Outcome
	}
	label := map[string]string{"check": "检查已有任务", "query": "查询并校验平台资料", "persist": "保存资料和指标", "target": "建立跟踪任务", "confirm": "确认最终状态"}[result.Stage]
	result.HTTPStatus, result.Code, result.Error = 502, "TARGET_SUBMISSION_FAILED", label+"失败，请核对状态后重试。"
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		result.Code = "REQUEST_TIMEOUT"
		result.HTTPStatus, result.Error = 504, label+"超时或已取消，请查询状态确认处理结果。"
	case errors.Is(err, tracker.ErrTargetStorage):
		result.HTTPStatus, result.Code, result.Error = 500, "INTERNAL_ERROR", label+"时发生数据库故障，请核对状态后重试。"
	case errors.Is(err, tracker.ErrTargetBusy):
		result.Code = "TARGET_BUSY"
		result.HTTPStatus, result.Error = 409, "目标正在采集中，请先刷新状态或稍后重试。"
	case errors.Is(err, tracker.ErrRequestCapacity):
		result.RetryAfter = 1
		result.Code = "RATE_LIMITED"
		result.HTTPStatus, result.Error = 429, "服务器采集任务较多，请稍后重试。"
	case errors.Is(err, tracker.ErrRequestStopping):
		result.HTTPStatus, result.Code, result.Error, result.RetryAfter = 429, "RATE_LIMITED", "服务器正在停止，请稍后核对状态。", 1
	}
	return result
}
