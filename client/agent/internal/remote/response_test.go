// response_test.go 验证HTTP200业务失败不被误认成功，并兼容旧服务器HTTP错误。
package remote

import (
	"context"
	"errors"
	"io"
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

func TestSubmissionFailurePreservesStageAndOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"code":"REQUEST_TIMEOUT","stage":"persist","outcome":"unknown","error":"保存超时，请核对"}`))
	}))
	defer server.Close()
	_, err := New(nil).Submit(context.Background(), server.URL, "creator", "1")
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "REQUEST_TIMEOUT" || failure.Stage != "persist" || failure.Outcome != "unknown" || failure.Target != nil {
		t.Fatal(err)
	}
}

func TestTransportTimeoutAfterSendingIsUnknownAndKeepsCause(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-entered; cancel() }()
	defer cancel()
	_, err := New(nil).Submit(ctx, server.URL, "creator", "1")
	var failure *Error
	if !errors.As(err, &failure) || failure.Stage != "transport" || failure.Outcome != "unknown" || failure.Code != "REQUEST_TIMEOUT" || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueueCancellationHasNotSentSubmission(t *testing.T) {
	client := New(nil)
	client.next = time.Now().Add(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Submit(ctx, "http://127.0.0.1:1", "creator", "1")
	var failure *Error
	if !errors.As(err, &failure) || failure.Stage != "transport" || failure.Outcome != "not_applied" || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
