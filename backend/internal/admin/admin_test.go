// admin_test.go 覆盖后台首页渲染和添加目标的表单流程。

package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

type noopQuerier struct{}

func (noopQuerier) GetVideo(context.Context, string, string) (source.Video, error) {
	return source.Video{}, &source.Error{Kind: source.UpstreamFailure}
}

func (noopQuerier) GetCreator(context.Context, string, string) (source.CreatorProfile, error) {
	return source.CreatorProfile{}, &source.Error{Kind: source.UpstreamFailure}
}

func (noopQuerier) ListCreatorVideos(context.Context, string, string) ([]source.CreatorVideo, error) {
	return nil, &source.Error{Kind: source.UpstreamFailure}
}

func TestIndexAndAddTarget(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	log, err := tracker.NewLog("", 20)
	if err != nil {
		t.Fatal(err)
	}
	trk := tracker.New(store, noopQuerier{}, log, "bilibili")
	server, err := New(store, trk, log, "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	// Do not follow the 303 redirect; the test asserts the POST result itself.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	response, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", response.StatusCode)
	}
	if len(body) == 0 {
		t.Fatal("index page is empty")
	}

	response, err = client.PostForm(ts.URL+"/targets", url.Values{
		"type": {"creator"}, "id": {"1"}, "interval_hours": {"3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add target status = %d, want 303", response.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		targets, err := store.ListTargets(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(targets) == 1 && targets[0].TargetID == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("target was not persisted: %+v", targets)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
