// client_history_test.go 验证限定ID的批量采样、平台隔离及不带聚合的资料兜底。
package storage

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMetricsBatchMatchesIndividualRanges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Truncate(24*time.Hour).AddDate(0, 0, -6)
	for _, sample := range []struct {
		platform, kind, id, metric string
		at                         time.Time
		value                      int64
	}{
		{"bilibili", "video", "one", "view", since, 100}, {"bilibili", "video", "one", "view", now, 20},
		{"bilibili", "video", "one", "view", since.Add(-time.Millisecond), 900}, {"bilibili", "video", "one", "view", now.Add(time.Millisecond), 900},
		{"other", "video", "one", "view", now, 999}, {"bilibili", "creator", "one", "follower", now, 999},
		{"bilibili", "video", "two", "view", now, 0},
	} {
		if err := s.RecordMetric(ctx, sample.platform, sample.kind, sample.id, sample.metric, sample.at, sample.value); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.MetricsBatch(ctx, "bilibili", "video", []string{"one", "two", "missing"}, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "missing"} {
		want, e := s.Metrics(ctx, "bilibili", "video", id, 7, now)
		if e != nil || !reflect.DeepEqual(got[id], want) {
			t.Fatal(id, got[id], want, e)
		}
	}
	if _, err := s.MetricsBatch(ctx, "bilibili", "video", []string{"one", "one"}, 7, now); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := s.MetricsBatch(ctx, "bilibili", "video", []string{"one"}, 30, now); err == nil {
		t.Fatal("unsupported batch range accepted")
	}
}

func TestMetadataBatchHasNoAuthorAggregates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	v := testVideo("本地失败时的兜底", 0, now)
	fp, _ := VideoFingerprint(v.Raw)
	if _, err := s.SaveVideo(ctx, v, fp); err != nil {
		t.Fatal(err)
	}
	p := testProfile("profile", 0, now)
	cfp, _ := CreatorFingerprint(p.Raw)
	if _, err := s.SaveCreator(ctx, p, cfp); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []struct{ kind, id string }{{"video", v.ID}, {"creator", p.ID}} {
		items, err := s.MetadataBatch(ctx, "bilibili", subject.kind, []string{subject.id, "missing"})
		item := items[subject.id]
		if err != nil || len(items) != 1 || item.Metric == nil || *item.Metric != 0 || item.Cover == "" || item.VideoViews != nil || item.RecentVideos != nil || item.Growth != nil {
			t.Fatal(item, err)
		}
		head, err := s.HistoryHead(ctx, "bilibili", subject.kind, subject.id)
		if err != nil || head.Seq != 1 || head.Name != "" || head.Metric != nil {
			t.Fatal(head, err)
		}
	}
}
