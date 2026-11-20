// detail_sources_test.go 用真实 SQLite 和驱动计数验证读取预算、状态及索引路径。
package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type detailCounter struct {
	driver.Driver
	count atomic.Int64
	query string
	args  []driver.NamedValue
}
type detailCountConn struct {
	driver.Conn
	owner *detailCounter
}

func (d *detailCounter) Open(name string) (driver.Conn, error) {
	c, e := d.Driver.Open(name)
	if e != nil {
		return nil, e
	}
	return &detailCountConn{c, d}, nil
}
func (c *detailCountConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.owner.count.Add(1)
	c.owner.query = q
	c.owner.args = append([]driver.NamedValue{}, args...)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

var detailDriverID atomic.Int64

func TestDetailSQLBudgetAndStates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	profile := testProfile("saved signature", 200, now.Add(-time.Hour))
	fp, _ := CreatorFingerprint(profile.Raw)
	if _, err := s.SaveCreator(ctx, profile, fp); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMetric(ctx, "bilibili", "creator", profile.ID, "follower", now.Truncate(24*time.Hour), 200); err != nil {
		t.Fatal(err)
	}
	d := &detailCounter{Driver: &sqlite.Driver{}}
	name := fmt.Sprintf("detail-budget-%d", detailDriverID.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, s.path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	reader := &Store{db: db, path: s.path}
	for _, state := range []string{"absent", "active", "paused"} {
		if state == "active" {
			if err = s.AddTarget(ctx, "bilibili", "creator", profile.ID, time.Hour, now); err != nil {
				t.Fatal(err)
			}
		}
		if state == "paused" {
			if _, err = s.db.Exec("UPDATE tracking_targets SET enabled=0"); err != nil {
				t.Fatal(err)
			}
		}
		d.count.Store(0)
		r, err := reader.ReadDetailTarget(ctx, "bilibili", "creator", profile.ID, now)
		if err != nil || !r.Recorded || r.Seq != 1 || r.TrackingState != state || r.Tracking != (state == "active") {
			t.Fatalf("state %s: %+v %v", state, r, err)
		}
		if d.count.Load() != 1 {
			t.Fatal("metadata query budget", d.count.Load())
		}
		checkDetailPlan(t, db, d)
		d.count.Store(1)
		series, err := reader.Metrics(ctx, "bilibili", "creator", profile.ID, 30, now)
		if err != nil || len(series.Points) != 1 || d.count.Load() != 2 {
			t.Fatalf("history budget: %+v %v %d", series, err, d.count.Load())
		}
		checkDetailPlan(t, db, d)
		d.count.Store(2)
		saved, err := reader.ReadSavedContent(ctx, "bilibili", "creator", profile.ID)
		if err != nil || saved.Item.Seq != 1 || saved.Description != "saved signature" || *saved.Item.Metric != 200 || d.count.Load() != 3 {
			t.Fatalf("fallback budget: %+v %v %d", saved, err, d.count.Load())
		}
		checkDetailPlan(t, db, d)
	}
	d.count.Store(0)
	r, err := reader.ReadDetailTarget(ctx, "bilibili", "creator", "999", now)
	if err != nil || r.Recorded || r.TrackingState != "absent" || d.count.Load() != 1 {
		t.Fatalf("missing %+v %v", r, err)
	}
	if _, err = reader.ReadSavedContent(ctx, "bilibili", "creator", "999"); !errors.Is(err, sql.ErrNoRows) || d.count.Load() != 2 {
		t.Fatal("missing fallback", err, d.count.Load())
	}
}

func checkDetailPlan(t *testing.T, db *sql.DB, d *detailCounter) {
	t.Helper()
	q := d.query
	args := make([]any, len(d.args))
	for i, v := range d.args {
		args[i] = v.Value
	}
	rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	search := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		search = search || strings.Contains(detail, "SEARCH")
		for _, table := range []string{"c", "t", "v", "x", "metric_samples"} {
			if strings.Contains(detail, "SCAN "+table+" ") || strings.HasSuffix(detail, "SCAN "+table) {
				t.Fatal("unexpected table scan", detail)
			}
		}
	}
	if err = rows.Err(); err != nil || !search {
		t.Fatal("no indexed lookup", err)
	}
}

func TestDetailPlaceholderVideoAndVersionCursor(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	video := testVideo("video", 0, now)
	fp, _ := VideoFingerprint(video.Raw)
	if _, err := s.SaveVideo(ctx, video, fp); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReadDetailTarget(ctx, "bilibili", "creator", video.Creator.ID, now)
	if err != nil || r.Recorded || r.Seq != 0 {
		t.Fatal(r, err)
	}
	if _, err = s.ReadSavedContent(ctx, "bilibili", "creator", video.Creator.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("placeholder was returned", err)
	}
	v, err := s.ReadSavedContent(ctx, "bilibili", "video", video.ID)
	if err != nil || v.Item.Seq != 1 || v.Item.OwnerID != video.Creator.ID || v.Item.Metric == nil || *v.Item.Metric != 0 {
		t.Fatal(v, err)
	}
	for i := 0; i < 12; i++ {
		p := testProfile(fmt.Sprintf("sign-%d", i), int64(i), now.Add(time.Duration(i+1)*time.Minute))
		hash, _ := CreatorFingerprint(p.Raw)
		if _, err = s.SaveCreator(ctx, p, hash); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ReadDetailVersions(ctx, "bilibili", "creator", "122541", 0)
	if err != nil || !page.HasMore || len(page.Items) != 10 || page.NextBefore != 3 {
		t.Fatal(page, err)
	}
	last, err := s.ReadDetailVersions(ctx, "bilibili", "creator", "122541", page.NextBefore)
	if err != nil || last.HasMore || len(last.Items) != 2 || last.Items[0].Seq != 2 || last.Items[1].Seq != 1 {
		t.Fatal(last, err)
	}
}
