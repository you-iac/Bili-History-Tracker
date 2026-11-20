// history_test.go 验证批量历史协议的身份、采样顺序和显式兜底边界。
package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBatchHistoryValidatesIdentityAndFallback(t *testing.T) {
	for _, body := range []string{
		`{"items":[{"kind":"creator","id":"2","points":[]}]}`,
		`{"items":[{"kind":"creator","id":"1","points":null}]}`,
		`{"items":[{"kind":"creator","id":"1","points":[{"at":2,"value":1},{"at":1,"value":2}]}]}`,
		`{"items":[{"kind":"creator","id":"1","points":[],"fallback":{"kind":"creator","id":"1"}}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
		_, err := New(nil).MetricsBatch(context.Background(), server.URL, "creator", []string{"1"}, nil)
		server.Close()
		if err == nil {
			t.Fatal("invalid response accepted", body)
		}
	}
}
