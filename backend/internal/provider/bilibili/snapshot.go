// snapshot.go 定义 B站视频存档边界，移除跨视频的合集详情并保留其余上游字段。
// 过滤规则版本仅在本模块代码中选择，不写入业务数据或数据库。
// 规则见 docs/database.md 的“payload 保存什么”。

package bilibili

import (
	"encoding/json"
	"errors"
)

// videoSnapshotVersion 选择本模块使用的过滤实现，与数据库和指纹版本无关。
// 版本 1 只删除顶层 ugc_season；将来切换规则时在此修改，不随每条快照传递。
const videoSnapshotVersion = 1

func videoSnapshot(raw json.RawMessage) (json.RawMessage, error) {
	switch videoSnapshotVersion {
	case 1:
		return videoSnapshotV1(raw)
	default:
		return nil, errors.New("unsupported video snapshot filter version")
	}
}

// videoSnapshotV1 不使用 videoData 重新构造快照，以免丢失分 P 和未来新增字段。
// RawMessage 保留嵌套对象和大整数，且不会递归删除其他路径的同名字段。
func videoSnapshotV1(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("video snapshot is not a JSON object")
	}
	if _, exists := fields["ugc_season"]; !exists {
		return raw, nil
	}
	delete(fields, "ugc_season")
	return json.Marshal(fields)
}
