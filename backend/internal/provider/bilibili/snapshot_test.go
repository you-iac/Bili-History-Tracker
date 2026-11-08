// snapshot_test.go 验证视频快照只裁剪顶层合集，并保持内容指纹、历史版本和详情读取兼容。
package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/admin"
	"bili-history-tracker/backend/internal/storage"
)

const snapshotVideoData = `{
	"bvid":"BV17x411w7KC","aid":9007199254740993,"title":"video",
	"desc":"<description>","desc_v2":[{"raw_text":"description"}],
	"pic":"https://example.com/cover.jpg","pubdate":1700000000,"duration":42,
	"owner":{"mid":12345,"name":"UP","face":"https://example.com/avatar.jpg"},
	"stat":{"view":100,"like":20},"dynamic":"dynamic text","state":0,
	"rights":{"download":1},"season_id":123,"is_season_display":true,
	"pages":[{"page":1,"cid":9007199254740993,"part":"P1","duration":20},
	         {"page":2,"cid":9007199254740994,"part":"P2","duration":22}],
	"subtitle":{"list":[{"id":9007199254740993,"lan":"zh-CN"}]},
	"staff":[{"mid":23456,"title":"staff"}],
	"future_field":{"large_id":9007199254740993,"nullable":null,
	                "ugc_season":{"keep":"nested field"}},
	"ugc_season":{"id":123,"title":"season","sections":[{"episodes":[
	    {"bvid":"BV17x411w7KC","title":"video"},
	    {"bvid":"BV1oa41137pj","title":"other video"}
	]}]}
}`

func snapshotObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestGetVideoSnapshot(t *testing.T) {
	for _, season := range []string{"present", "absent", "null"} {
		t.Run(season, func(t *testing.T) {
			input := snapshotObject(t, []byte(snapshotVideoData))
			switch season {
			case "absent":
				delete(input, "ugc_season")
			case "null":
				input["ugc_season"] = nil
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			video, err := withResponse(http.StatusOK, `{"code":0,"data":`+string(raw)+`}`).GetVideo(context.Background(), testBVID)
			if err != nil {
				t.Fatal(err)
			}
			delete(input, "ugc_season")
			// 比较所有字段及嵌套值，确保只删除一个顶层字段，大整数不经浮点转换。
			if got := snapshotObject(t, video.Raw); !reflect.DeepEqual(got, input) {
				t.Fatalf("snapshot changed unrelated fields: got %v, want %v", got, input)
			}
			before, err := storage.VideoFingerprint(raw)
			if err != nil {
				t.Fatal(err)
			}
			after, err := storage.VideoFingerprint(video.Raw)
			if err != nil || before != after {
				t.Fatalf("snapshot changed content fingerprint: %s -> %s, err=%v", before, after, err)
			}
			apiJSON, err := json.Marshal(video)
			if err != nil {
				t.Fatal(err)
			}
			apiObject := snapshotObject(t, apiJSON)
			for _, key := range []string{"Raw", "raw", "SnapshotVersion", "snapshot_version"} {
				if _, exists := apiObject[key]; exists {
					t.Fatalf("persistence metadata leaked into video API: %s", apiJSON)
				}
			}
		})
	}
}

func TestCreatorSnapshotRemainsComplete(t *testing.T) {
	raw := `{"card":{"mid":"122541","name":"UP"},"ugc_season":{"keep":true},"future_field":9007199254740993}`
	profile, err := withResponse(http.StatusOK, `{"code":0,"data":`+raw+`}`).GetCreator(context.Background(), "122541")
	if err != nil {
		t.Fatal(err)
	}
	if string(profile.Raw) != raw {
		t.Fatalf("creator snapshot was changed: %s", profile.Raw)
	}
}

func TestVideoSnapshotPersistence(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "first observation"
		if legacy {
			name = "existing full snapshot"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, err := storage.Open(filepath.Join(t.TempDir(), "snapshot.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			video, err := withResponse(http.StatusOK, `{"code":0,"data":`+snapshotVideoData+`}`).GetVideo(ctx, testBVID)
			if err != nil {
				t.Fatal(err)
			}
			video.ObservedAt = time.Unix(1800000000, 0).UTC()
			fingerprint, err := storage.VideoFingerprint(video.Raw)
			if err != nil {
				t.Fatal(err)
			}
			wantSeq := int64(1)
			if legacy {
				old := video
				old.Raw = json.RawMessage(snapshotVideoData)
				if _, err := store.SaveVideo(ctx, old, fingerprint); err != nil {
					t.Fatal(err)
				}
				video.ObservedAt = video.ObservedAt.Add(time.Hour)
				result, err := store.SaveVideo(ctx, video, fingerprint)
				if err != nil || result.VersionAdded || result.Seq != 1 {
					t.Fatalf("format change added a content version: %+v, err=%v", result, err)
				}
				detail, err := store.LatestVideo(ctx, platform, testBVID)
				if err != nil || string(detail.Payload) != snapshotVideoData {
					t.Fatalf("legacy snapshot was rewritten: %+v, err=%v", detail, err)
				}
				// 真实内容变化后，才写入采用新快照格式的下一版本。
				video.Title = "changed"
				video.Raw = bytes.Replace(video.Raw, []byte(`"title":"video"`), []byte(`"title":"changed"`), 1)
				video.ObservedAt = video.ObservedAt.Add(time.Hour)
				fingerprint, err = storage.VideoFingerprint(video.Raw)
				if err != nil {
					t.Fatal(err)
				}
				wantSeq = 2
			}
			result, err := store.SaveVideo(ctx, video, fingerprint)
			if err != nil || !result.VersionAdded || result.Seq != wantSeq {
				t.Fatalf("save snapshot: %+v, err=%v", result, err)
			}
			detail, err := store.LatestVideo(ctx, platform, testBVID)
			if err != nil || !bytes.Equal(detail.Payload, video.Raw) {
				t.Fatalf("snapshot round trip failed: %+v, err=%v", detail, err)
			}
			if _, exists := snapshotObject(t, detail.Payload)["ugc_season"]; exists {
				t.Fatal("saved snapshot contains season data")
			}
			var hashVersion int
			if err := store.DB().QueryRow(`SELECT hash_version FROM video_versions WHERE seq = ?`, wantSeq).Scan(&hashVersion); err != nil || hashVersion != 1 {
				t.Fatalf("hash version changed with snapshot format: %d, err=%v", hashVersion, err)
			}
			server, err := admin.New(store, nil, nil, platform)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/detail/video/"+testBVID, nil))
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), testBVID) || strings.Contains(recorder.Body.String(), "other video") {
				t.Fatalf("unexpected video detail page: status=%d, body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
