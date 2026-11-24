// interactions_test.go 验证互动自动关注设置的读取、校验与手动执行。
package httpapi

import "testing"

func TestInteractionSettingsRoutes(t *testing.T) {
	h := setup(t)
	if r := h.call("GET", "/api/interactions", ""); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	valid := `{"enabled":true,"sources":["like","coin"],"interval_minutes":60,"max_auto_follow":20}`
	if r := h.call("PUT", "/api/interactions", valid); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := h.call("PUT", "/api/config/cookie", `{"cookie":"SESSDATA=test-only"}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	invalid := `{"enabled":true,"sources":["bad"],"interval_minutes":60,"max_auto_follow":20}`
	if r := h.call("PUT", "/api/interactions", invalid); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := h.call("POST", "/api/interactions/run", "{}"); r.Code != 500 {
		t.Fatal(r.Code, r.Body.String())
	}
}
