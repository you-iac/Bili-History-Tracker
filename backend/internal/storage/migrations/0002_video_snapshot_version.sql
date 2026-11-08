-- 0002: 标记适配器的视频快照格式，独立于内容指纹 hash_version。
-- 历史完整 data 默认为 0；仅增加元数据，不裁剪 payload 或制造新版本。
ALTER TABLE video_versions ADD COLUMN snapshot_version INTEGER NOT NULL DEFAULT 0
    CHECK (snapshot_version >= 0);
