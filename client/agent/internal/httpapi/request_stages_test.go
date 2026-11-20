// request_stages_test.go 验证本地错误应答保留服务器阶段和结果状态，不转发底层诊断信息。
package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"bili-history-tracker/client/agent/internal/remote"
)

func TestRemoteFailureStageReachesBrowser(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, &remote.Error{Status: 502, Code: "REQUEST_TIMEOUT", Stage: "persist", Outcome: "unknown", Message: "保存超时"})
	var reply struct {
		Error struct{ Code, Stage, Outcome, Message string }
	}
	if w.Code != 502 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Error.Code != "REQUEST_TIMEOUT" || reply.Error.Stage != "persist" || reply.Error.Outcome != "unknown" || reply.Error.Message != "保存超时" {
		t.Fatal(w.Code, w.Body.String())
	}
}
