// response_test.go 验证HTTP200业务失败不被误认成功，并兼容旧服务器HTTP错误。
package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBusinessJSONFailurePreservesCodeAndTarget(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{{200, `{"ok":false,"code":"DETAIL_NOT_RECORDED","error":"无资料","target":{"kind":"video","id":"BV17x411w7KC","tracking_state":"active","tracking":true,"recorded":false,"status":"pending"}}`, "DETAIL_NOT_RECORDED"}, {200, `{"ok":false,"code":"RATE_LIMITED","error":"busy","retry_after_seconds":2}`, "RATE_LIMITED"}, {404, `{"error":"旧库无资料"}`, "DETAIL_NOT_RECORDED"}, {200, `{"ok":false,"code":"TARGET_SUBMISSION_FAILED","error":"失败"}`, "TARGET_SUBMISSION_FAILED"}} {
		t.Run(tc.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New(nil)
			_, err := client.Detail(context.Background(), server.URL, "video", "BV17x411w7KC", 1, 0)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatal(err)
			}
			if tc.status == 200 && tc.code == "DETAIL_NOT_RECORDED" && (failure.Target == nil || failure.Target.TrackingState != "active") {
				t.Fatal(failure)
			}
			if tc.code == "RATE_LIMITED" && (failure.RetryAfter != 2*time.Second || time.Until(client.next) < time.Second) {
				t.Fatal("lost shared cooldown", failure, client.next)
			}
		})
	}
}

func TestSubmitBusinessFailureNeverConfirmsTracking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"code":"TARGET_PAUSED","error":"暂停","tracking":true}`))
	}))
	defer server.Close()
	if _, err := New(nil).Submit(context.Background(), server.URL, "video", "BV17x411w7KC"); err == nil {
		t.Fatal("HTTP200 business failure accepted")
	}
}
