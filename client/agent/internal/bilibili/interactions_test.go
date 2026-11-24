// interactions_test.go 验证点赞/投币/收藏集合的请求与解析。
package bilibili

import (
	"context"
	"net/http"
	"testing"
)

func TestHasLikeAndCoins(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/x/web-interface/archive/has/like":
			return response(`{"code":0,"data":1}`, 200), nil
		case "/x/web-interface/archive/coins":
			return response(`{"code":0,"data":{"multiply":2}}`, 200), nil
		}
		t.Error("unexpected path:", r.URL.Path)
		return response(`{"code":-404}`, 404), nil
	})})
	like, err := client.HasLike(context.Background(), "SESSDATA=test", "BV17x411w7KC", keys)
	if err != nil || !like {
		t.Fatal(like, err)
	}
	coin, err := client.Coins(context.Background(), "SESSDATA=test", "BV17x411w7KC", keys)
	if err != nil || coin != 2 {
		t.Fatal(coin, err)
	}
}

func TestFavoriteSetPaginates(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/x/v3/fav/folder/created/list-all":
			return response(`{"code":0,"data":{"list":[{"id":1,"media_count":2}]}}`, 200), nil
		case "/x/v3/fav/resource/list":
			return response(`{"code":0,"data":{"medias":[{"bvid":"BV17x411w7KC"}],"has_more":false}}`, 200), nil
		}
		t.Error("unexpected path:", r.URL.Path)
		return response(`{"code":-404}`, 404), nil
	})})
	set, err := client.FavoriteSet(context.Background(), "SESSDATA=test", 123, keys)
	if err != nil || !set["BV17x411w7KC"] || len(set) != 1 {
		t.Fatal(set, err)
	}
}

func TestArchiveRelation(t *testing.T) {
	keys, _ := NewKeys(img, sub)
	client := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/x/web-interface/archive/relation" {
			t.Error("unexpected path:", r.URL.Path)
		}
		return response(`{"code":0,"data":{"like":true,"coin":2,"favorite":true,"attention":false}}`, 200), nil
	})})
	relation, err := client.ArchiveRelation(context.Background(), "SESSDATA=test", "BV17x411w7KC", keys)
	if err != nil || !relation.Like || relation.Coin != 2 || !relation.Favorite {
		t.Fatal(relation, err)
	}
}
