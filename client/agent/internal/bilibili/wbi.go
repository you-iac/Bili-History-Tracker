// wbi.go 实现投稿接口的 WBI 参数签名；仅从 nav 返回值提取材料，不下载图片。
package bilibili

import (
	"crypto/md5" // WBI 协议要求；不用于本地凭据保护。
	"encoding/hex"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type Keys struct{ mixin string }

var mixinOrder = [...]int{46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52}

func NewKeys(imageURL, subURL string) (Keys, error) {
	var combined string
	for _, raw := range []string{imageURL, subURL} {
		u, err := url.Parse(raw)
		if err != nil {
			return Keys{}, &Error{Code: "invalid_response", Message: "无法获取投稿接口签名材料。"}
		}
		name := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
		if len(name) != 32 {
			return Keys{}, &Error{Code: "invalid_response", Message: "投稿接口签名材料无效。"}
		}
		if _, err := hex.DecodeString(name); err != nil {
			return Keys{}, &Error{Code: "invalid_response", Message: "投稿接口签名材料无效。"}
		}
		combined += name
	}
	var mixed strings.Builder
	for _, i := range mixinOrder[:32] {
		mixed.WriteByte(combined[i])
	}
	return Keys{mixin: mixed.String()}, nil
}

// Sign 复制并签名；与 encodeURIComponent 一致，空格使用 %20。
func (k Keys) Sign(params url.Values, now time.Time) url.Values {
	result := url.Values{}
	for name, values := range params {
		if name == "w_rid" || name == "wts" {
			continue
		}
		for _, value := range values {
			value = strings.Map(func(r rune) rune {
				if strings.ContainsRune("!'()*", r) {
					return -1
				}
				return r
			}, value)
			result.Add(name, value)
		}
	}
	result.Set("wts", strconv.FormatInt(now.Unix(), 10))
	encoded := strings.ReplaceAll(result.Encode(), "+", "%20")
	hash := md5.Sum([]byte(encoded + k.mixin))
	result.Set("w_rid", hex.EncodeToString(hash[:]))
	return result
}
