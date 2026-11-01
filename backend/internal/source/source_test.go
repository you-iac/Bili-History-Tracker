package source

import (
	"context"
	"errors"
	"testing"
)

type stubProvider struct{ name string }

func (p stubProvider) Platform() string { return p.name }
func (p stubProvider) GetVideo(_ context.Context, id string) (Video, error) {
	return Video{Platform: p.name, ID: id}, nil
}

func TestDispatchSupportsMultiplePlatforms(t *testing.T) {
	s, err := NewService(stubProvider{"bilibili"}, stubProvider{"another-platform"})
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"bilibili", "another-platform"} {
		video, err := s.GetVideo(context.Background(), platform, "opaque-id")
		if err != nil || video.Platform != platform || video.ID != "opaque-id" {
			t.Fatalf("dispatch %s: %+v, %v", platform, video, err)
		}
	}
	for _, tc := range []struct {
		platform, id string
		kind         ErrorKind
	}{
		{"unknown", "123", UnsupportedPlatform},
		{"", "123", InvalidInput},
		{"bilibili", "", InvalidInput},
	} {
		_, err := s.GetVideo(context.Background(), tc.platform, tc.id)
		var queryError *Error
		if !errors.As(err, &queryError) || queryError.Kind != tc.kind {
			t.Fatalf("GetVideo(%q, %q) = %v", tc.platform, tc.id, err)
		}
	}
}

func TestRejectInvalidRegistration(t *testing.T) {
	for _, providers := range [][]Provider{
		{nil}, {stubProvider{""}}, {stubProvider{"bilibili"}, stubProvider{"bilibili"}},
	} {
		if _, err := NewService(providers...); err == nil {
			t.Fatal("invalid registration was accepted")
		}
	}
}
