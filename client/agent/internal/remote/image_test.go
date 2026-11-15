// image_test.go 验证快照图片提取与CDN地址边界，确保不接受任意URL或凭据。
package remote

import (
	"encoding/json"
	"testing"
)

func TestImageURLAndSnapshots(t *testing.T) {
	for _, input := range []string{"http://i0.hdslb.com/a.jpg#fragment", "//i0.hdslb.com/a.jpg"} {
		if got := NormalizeImageURL(input); got != "https://i0.hdslb.com/a.jpg" {
			t.Fatal(input, got)
		}
	}
	for _, input := range []string{"https://i0.hdslb.com.evil.test/x", "https://evilhdslb.com/x", "https://localhost/x", "https://u:p@i0.hdslb.com/x", "https://i0.hdslb.com:8443/x", "file:///tmp/x", "data:image/png,x", "/img/x", "https://i0.hdslb.com\\@evil.test/x"} {
		if got := NormalizeImageURL(input); got != "" {
			t.Fatal("untrusted URL accepted", input, got)
		}
	}
	raw := json.RawMessage(`{"card":{"face":"https://i0.hdslb.com/face.jpg"},"pic":"https://i1.hdslb.com/cover.jpg"}`)
	if SnapshotImageURL("creator", raw) != "https://i0.hdslb.com/face.jpg" || SnapshotImageURL("video", raw) != "https://i1.hdslb.com/cover.jpg" {
		t.Fatal("wrong image source")
	}
	for _, raw := range []json.RawMessage{nil, []byte(`{"pic":123}`), []byte(`{"pic":"https://evil.test/a"}`)} {
		if SnapshotImageURL("video", raw) != "" {
			t.Fatal("invalid image accepted", string(raw))
		}
	}
}
