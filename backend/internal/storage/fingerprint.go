// fingerprint.go 从适配器准备的业务快照中投影稳定内容字段，计算 8 字节内容指纹。
// 指纹只用于同一目标相邻版本比较；投影清单和长度由 fingerprintVersion 固化。
// 统计、状态、作者昵称等字段保留在 payload 中但不参与指纹。
//
// 注意：下面的字段路径（owner.mid、pages[]、card.sign 等）目前是 B站 view/card
// 的 data 格式；新增平台时需要按平台扩展投影，不能直接复用这份清单。

package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	// FingerprintVersion 对应 docs/database.md 的 hash_version = 1。
	FingerprintVersion = 1

	fingerprintPrefix = "bht-content-v1\n"
)

// Fingerprint 是当前表和版本表共用的 8 字节内容指纹。
type Fingerprint [8]byte

// ErrInvalidPayload 表示上游 data 不是可解析的 JSON 对象。
var ErrInvalidPayload = errors.New("payload is not a JSON object")

// String 返回十六进制形式，仅用于日志和调试。
func (f Fingerprint) String() string { return hex.EncodeToString(f[:]) }

// Equal 提供不依赖数组比较语法的可读形式。
func (f Fingerprint) Equal(other Fingerprint) bool { return f == other }

type videoProjection struct {
	Title    string           `json:"title"`
	Desc     string           `json:"desc"`
	Pic      string           `json:"pic"`
	Pubdate  int64            `json:"pubdate_ms"`
	Duration int64            `json:"duration_seconds"`
	OwnerMID string           `json:"owner_mid"`
	Dynamic  string           `json:"dynamic"`
	Pages    []pageProjection `json:"pages"`
}

type pageProjection struct {
	Page     int64  `json:"page"`
	CID      int64  `json:"cid"`
	Part     string `json:"part"`
	Duration int64  `json:"duration_seconds"`
}

type creatorProjection struct {
	Name string `json:"name"`
	Sign string `json:"sign"`
	Face string `json:"face"`
	MID  string `json:"mid"`
}

// VideoFingerprint 从 view 接口的 data 对象计算视频内容指纹。
func VideoFingerprint(raw []byte) (Fingerprint, error) {
	root, err := decodeObject(raw)
	if err != nil {
		return Fingerprint{}, err
	}
	owner := childObject(root, "owner")
	return digest(videoProjection{
		Title:    stringField(root, "title"),
		Desc:     stringField(root, "desc"),
		Pic:      normalizeURL(stringField(root, "pic")),
		Pubdate:  intField(root, "pubdate") * 1000,
		Duration: intField(root, "duration"),
		OwnerMID: numberField(owner, "mid"),
		Dynamic:  stringField(root, "dynamic"),
		Pages:    pageProjections(root),
	})
}

// CreatorFingerprint 从 card 接口的 data 对象计算作者资料指纹。
func CreatorFingerprint(raw []byte) (Fingerprint, error) {
	root, err := decodeObject(raw)
	if err != nil {
		return Fingerprint{}, err
	}
	card := childObject(root, "card")
	return digest(creatorProjection{
		Name: stringField(card, "name"),
		Sign: stringField(card, "sign"),
		Face: normalizeURL(stringField(card, "face")),
		MID:  numberField(card, "mid"),
	})
}

func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrInvalidPayload
	}
	return value, nil
}

func childObject(parent map[string]any, key string) map[string]any {
	child, _ := parent[key].(map[string]any)
	return child
}

func stringField(obj map[string]any, key string) string {
	value, _ := obj[key].(string)
	return value
}

func numberField(obj map[string]any, key string) string {
	if number, ok := obj[key].(json.Number); ok {
		return number.String()
	}
	return ""
}

func intField(obj map[string]any, key string) int64 {
	number, ok := obj[key].(json.Number)
	if !ok {
		return 0
	}
	value, err := number.Int64()
	if err != nil {
		return 0
	}
	return value
}

func pageProjections(root map[string]any) []pageProjection {
	items, ok := root["pages"].([]any)
	if !ok {
		return []pageProjection{}
	}
	pages := make([]pageProjection, 0, len(items))
	for _, item := range items {
		page, ok := item.(map[string]any)
		if !ok {
			continue
		}
		pages = append(pages, pageProjection{
			Page:     intField(page, "page"),
			CID:      intField(page, "cid"),
			Part:     stringField(page, "part"),
			Duration: intField(page, "duration"),
		})
	}
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].Page != pages[j].Page {
			return pages[i].Page < pages[j].Page
		}
		return pages[i].CID < pages[j].CID
	})
	return pages
}

// normalizeURL 去掉查询参数和图片处理后缀，避免签名参数变化被当成换封面。
func normalizeURL(raw string) string {
	if index := strings.IndexByte(raw, '?'); index >= 0 {
		raw = raw[:index]
	}
	if index := strings.IndexByte(raw, '@'); index >= 0 {
		raw = raw[:index]
	}
	return raw
}

func digest(value any) (Fingerprint, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return Fingerprint{}, err
	}
	hasher := sha256.New()
	hasher.Write([]byte(fingerprintPrefix))
	hasher.Write(canonical)
	var fingerprint Fingerprint
	copy(fingerprint[:], hasher.Sum(nil))
	return fingerprint, nil
}
