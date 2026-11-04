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

type creatorProviderStub struct {
	stubProvider
	query func(context.Context, string) (CreatorProfile, error)
}

func (p creatorProviderStub) GetCreator(ctx context.Context, id string) (CreatorProfile, error) {
	return p.query(ctx, id)
}

func TestCreatorDispatchAndCapabilities(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstreamErr := &Error{Kind: AccessRestricted, Platform: "blocked"}
	s, err := NewService(
		stubProvider{"video-only"},
		creatorProviderStub{stubProvider{"another-platform"}, func(gotCtx context.Context, id string) (CreatorProfile, error) {
			if gotCtx != ctx || id != "opaque-creator-id" {
				t.Fatalf("incorrect creator query: %v, %q", gotCtx, id)
			}
			return CreatorProfile{Platform: "another-platform", ID: id, Name: "测试作者"}, nil
		}},
		creatorProviderStub{stubProvider{"blocked"}, func(context.Context, string) (CreatorProfile, error) {
			return CreatorProfile{}, upstreamErr
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.GetCreator(ctx, "another-platform", "opaque-creator-id")
	if err != nil || profile.Platform != "another-platform" || profile.ID != "opaque-creator-id" || profile.Name != "测试作者" {
		t.Fatalf("incorrect creator dispatch: %+v, %v", profile, err)
	}
	for _, tc := range []struct {
		platform, id string
		kind         ErrorKind
	}{
		{"video-only", "123", UnsupportedOperation},
		{"unknown", "123", UnsupportedPlatform},
		{"", "123", InvalidInput},
		{"another-platform", "", InvalidInput},
	} {
		_, err := s.GetCreator(ctx, tc.platform, tc.id)
		var queryError *Error
		if !errors.As(err, &queryError) || queryError.Kind != tc.kind {
			t.Fatalf("GetCreator(%q, %q) = %v", tc.platform, tc.id, err)
		}
	}
	_, err = s.GetCreator(ctx, "blocked", "123")
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("provider error lost: %v", err)
	}
	// Adding an optional capability must not stop video-only providers working.
	video, err := s.GetVideo(ctx, "video-only", "opaque-video-id")
	if err != nil || video.ID != "opaque-video-id" {
		t.Fatalf("video-only provider regressed: %+v, %v", video, err)
	}
}
