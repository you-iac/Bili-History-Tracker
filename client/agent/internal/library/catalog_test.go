// catalog_test.go 验证链接等价、全量排序后分页、未知末尾及扫描关注归属。
package library

import (
	"fmt"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestCatalogLinkSearchAndGlobalSort(t *testing.T) {
	s, _ := New(&failingStore{}, remote.New(nil))
	for n := 1; n <= 25; n++ {
		metric := int64(n)
		i := remote.Item{Kind: "creator", ID: fmt.Sprint(n), Name: fmt.Sprintf("创作者 %02d", n), Metric: &metric, PublishedAt: int64(n)}
		if n == 25 {
			i.Metric = nil
		}
		s.state.Follows = append(s.state.Follows, Follow{Kind: "creator", ID: i.ID, AddedAt: time.Now(), Data: &remote.Detail{Item: i}})
	}
	first := s.ListCatalog("creator", "", 1, 0, false, "popular", "")
	second := s.ListCatalog("creator", "", 2, 0, false, "popular", "")
	if first.Total != 25 || first.Items[0].ID != "24" || second.Items[len(second.Items)-1].ID != "25" {
		t.Fatalf("sort must precede pagination, unknown last: %+v %+v", first, second)
	}
	if got := s.ListCatalog("creator", "", 1, 0, false, "published", ""); got.Items[0].ID != "25" {
		t.Fatal("latest publication sort", got)
	}
	for _, q := range []string{"12", "https://space.bilibili.com/12", "https://space.bilibili.com/12?spm_id_from=333.788.upinfo.detail.click"} {
		got := s.ListCatalog("creator", q, 1, 0, false, "popular", "")
		// Numeric identifiers are exact, even when another UID contains the same digits.
		if got.Total != 1 || got.Items[0].ID != "12" {
			t.Fatalf("link query %s: %+v", q, got)
		}
	}
	for _, q := range []string{"https://space.bilibili.com.evil/12", "https://example.com/video/BV17x411w7KC"} {
		if _, err := SearchQuery("creator", q); err == nil {
			t.Fatal("untrusted URL accepted", q)
		}
	}
	for _, q := range []string{"BV17x411w7KC", "https://www.bilibili.com/video/BV17x411w7KC?spm_id_from=abc"} {
		if id, err := SearchQuery("video", q); err != nil || id != "BV17x411w7KC" {
			t.Fatal(q, id, err)
		}
	}
}

func TestDiscoveryContextSurvivesRestart(t *testing.T) {
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	video := bilibili.Video{ID: "BV17x411w7KC", Title: "尚未读取服务器的投稿", PublishedAt: 1700000000}
	if _, err = s.Add("video", video.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RememberDiscovery(video.ID, "123", video); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	got := restarted.ListCatalog("video", "", 1, 0, false, "published", "123")
	if got.Total != 1 || got.Items[0].Data.Item.Name != video.Title || !got.Items[0].Pending {
		t.Fatal("restart lost pending discovery context", got)
	}
}

func TestScanCatalogFollowTransitionAndCreatorBoundary(t *testing.T) {
	s, _ := New(&failingStore{}, remote.New(nil))
	views := int64(999)
	videos := []bilibili.Video{{ID: "BV17x411w7KC", Title: "新投稿", PublishedAt: 200, Views: &views}, {ID: "BV1oa41137pj", Title: "旧投稿", PublishedAt: 100}}
	got := s.ScanCatalog(videos, "123", "", "views", 1, 0)
	if got.Total != 2 || got.Items[0].ID != videos[0].ID || got.Items[1].Metric != nil {
		t.Fatal(got)
	}
	if _, err := s.Add("video", videos[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := s.ScanCatalog(videos, "123", "", "published", 1, 0); got.Total != 1 || got.Items[0].ID != videos[1].ID {
		t.Fatal("follow not removed from discovery", got)
	}
	gotFollowed := s.ListCatalog("video", "", 1, 0, false, "published", "123", videos)
	if gotFollowed.Total != 1 || gotFollowed.Items[0].Data.Item.Name != "新投稿" {
		t.Fatal("follow without remote cache lost scan context", gotFollowed)
	}
	if got := s.ListCatalog("video", "", 1, 0, false, "published", "456"); got.Total != 0 {
		t.Fatal("other creator leaked", got)
	}
	if _, err := s.RememberDiscovery(videos[0].ID, "123", videos[0]); err != nil {
		t.Fatal(err)
	}
	// 即使原扫描被其他 UP 的任务替换，待提交关注仍保留标题和 UP 关联。
	if got := s.ListCatalog("video", "", 1, 0, false, "published", "123"); got.Total != 1 || got.Items[0].Data.Item.Name != "新投稿" {
		t.Fatal("discovery context lost after scan replacement", got)
	}
	if cmp := compareItems(remote.Item{PublishedAt: 1700000001}, remote.Item{PublishedAt: 1700000000000}, "published"); cmp >= 0 {
		t.Fatal("seconds and milliseconds not normalized")
	}
	_ = s.Remove("video", videos[0].ID)
	if got := s.ScanCatalog(videos, "123", "https://www.bilibili.com/video/BV17x411w7KC?x=1", "published", 1, 0); got.Total != 1 {
		t.Fatal("unfollow or link search failed", got)
	}
}
