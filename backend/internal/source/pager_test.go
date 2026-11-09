// pager_test.go 验证分页能力按平台分发且不要求平台具有合集概念。
package source

import (
	"context"
	"errors"
	"testing"
)

type pagerStub struct {
	stubProvider
	query func(context.Context, string, string) (CreatorVideoPage, error)
}

func (p pagerStub) GetCreatorVideoPage(ctx context.Context, id, cursor string) (CreatorVideoPage, error) {
	return p.query(ctx, id, cursor)
}
func TestVideoPagerDispatchAndOptionalCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := errors.New("upstream")
	service, err := NewService(stubProvider{"video-only"}, pagerStub{stubProvider{"generic"}, func(got context.Context, id, cursor string) (CreatorVideoPage, error) {
		if got != ctx || id != "author" || cursor != "opaque" {
			t.Fatal("query arguments changed")
		}
		return CreatorVideoPage{NextCursor: "next"}, upstream
	}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.GetCreatorVideoPage(ctx, "generic", "author", "opaque")
	if !errors.Is(err, upstream) || page.NextCursor != "next" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	_, err = service.GetCreatorVideoPage(ctx, "video-only", "author", "")
	var se *Error
	if !errors.As(err, &se) || se.Kind != UnsupportedOperation {
		t.Fatalf("unsupported pager: %v", err)
	}
	_, err = service.GetCreatorVideoPage(ctx, "missing", "author", "")
	if !errors.As(err, &se) || se.Kind != UnsupportedPlatform {
		t.Fatalf("unknown platform: %v", err)
	}
}
