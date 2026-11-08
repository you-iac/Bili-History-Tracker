-- 0004: 每个关注目标只保留一份最近采集状态；异常历史仍由查询日志承载。
-- 迁移不改变内容快照、指纹和 UTC 日采样粒度，旧目标通过已有成功观测补齐时间。
ALTER TABLE tracking_targets ADD COLUMN last_result TEXT NOT NULL DEFAULT '';
ALTER TABLE tracking_targets ADD COLUMN last_success_at INTEGER;
ALTER TABLE tracking_targets ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0);
ALTER TABLE tracking_targets ADD COLUMN last_failure_at INTEGER;
ALTER TABLE tracking_targets ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE tracking_targets ADD COLUMN recovered_at INTEGER;
UPDATE tracking_targets SET last_success_at = CASE target_type
    WHEN 'video' THEN (SELECT last_observed_at FROM videos v WHERE v.platform = tracking_targets.platform AND v.video_id = tracking_targets.target_id)
    ELSE (SELECT last_profile_observed_at FROM creators c WHERE c.platform = tracking_targets.platform AND c.creator_id = tracking_targets.target_id) END;
CREATE INDEX idx_metrics_window ON metric_samples(platform, metric, sampled_at);
CREATE INDEX idx_video_versions_observed ON video_versions(platform, observed_at);
CREATE INDEX idx_creator_versions_observed ON creator_versions(platform, observed_at);
