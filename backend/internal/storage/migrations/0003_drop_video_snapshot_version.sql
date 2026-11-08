-- 0003: 过滤规则版本只由适配器代码管理，不作为每条视频快照的存储元数据。
-- 保留已执行的 0002 迁移；删除多余列，不裁剪 payload 或改变内容指纹、版本序号。
ALTER TABLE video_versions DROP COLUMN snapshot_version;
