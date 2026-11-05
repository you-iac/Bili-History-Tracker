-- 0001: 当前索引、内容版本、指标采样与调度目标。
-- 表结构对应 docs/database.md；tracking_targets 是调度配置，不属于内容设计。
PRAGMA application_id = 1112036401;

CREATE TABLE creators (
    platform                  TEXT NOT NULL CHECK (length(platform) > 0),
    creator_id                TEXT NOT NULL CHECK (length(creator_id) > 0),
    name                      TEXT NOT NULL,
    follower_count            INTEGER CHECK (follower_count >= 0),
    first_seen_at             INTEGER NOT NULL CHECK (first_seen_at >= 0),
    last_referenced_at        INTEGER NOT NULL CHECK (last_referenced_at >= first_seen_at),
    last_profile_observed_at  INTEGER CHECK (last_profile_observed_at >= first_seen_at
                                             AND last_profile_observed_at <= last_referenced_at),
    current_profile_hash      BLOB CHECK (current_profile_hash IS NULL
                                          OR length(current_profile_hash) = 8),
    current_profile_seq       INTEGER CHECK (current_profile_seq IS NULL
                                             OR current_profile_seq >= 1),
    PRIMARY KEY (platform, creator_id),
    CHECK ((last_profile_observed_at IS NULL) = (current_profile_hash IS NULL)),
    CHECK ((current_profile_hash IS NULL) = (current_profile_seq IS NULL)),
    CHECK (follower_count IS NULL OR current_profile_hash IS NOT NULL)
);

CREATE TABLE videos (
    platform          TEXT NOT NULL CHECK (length(platform) > 0),
    video_id          TEXT NOT NULL CHECK (length(video_id) > 0),
    creator_id        TEXT NOT NULL CHECK (length(creator_id) > 0),
    title             TEXT NOT NULL,
    published_at      INTEGER CHECK (published_at >= 0),
    duration_seconds  INTEGER NOT NULL CHECK (duration_seconds >= 0),
    view_count        INTEGER CHECK (view_count >= 0),
    first_seen_at     INTEGER NOT NULL CHECK (first_seen_at >= 0),
    last_observed_at  INTEGER NOT NULL CHECK (last_observed_at >= first_seen_at),
    current_hash      BLOB NOT NULL CHECK (length(current_hash) = 8),
    current_seq       INTEGER NOT NULL CHECK (current_seq >= 1),
    PRIMARY KEY (platform, video_id),
    FOREIGN KEY (platform, creator_id)
        REFERENCES creators (platform, creator_id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE TABLE creator_versions (
    platform          TEXT NOT NULL CHECK (length(platform) > 0),
    creator_id        TEXT NOT NULL CHECK (length(creator_id) > 0),
    seq               INTEGER NOT NULL CHECK (seq >= 1),
    observed_at       INTEGER NOT NULL CHECK (observed_at >= 0),
    source_url        TEXT NOT NULL,
    content_hash      BLOB NOT NULL CHECK (length(content_hash) = 8),
    hash_version      INTEGER NOT NULL CHECK (hash_version >= 1),
    payload_encoding  TEXT NOT NULL DEFAULT 'json'
                      CHECK (payload_encoding IN ('json', 'zstd')),
    payload           TEXT NOT NULL,
    PRIMARY KEY (platform, creator_id, seq),
    UNIQUE (platform, creator_id, observed_at),
    CHECK (payload_encoding <> 'json' OR json_valid(payload)),
    FOREIGN KEY (platform, creator_id)
        REFERENCES creators (platform, creator_id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE TABLE video_versions (
    platform          TEXT NOT NULL CHECK (length(platform) > 0),
    video_id          TEXT NOT NULL CHECK (length(video_id) > 0),
    seq               INTEGER NOT NULL CHECK (seq >= 1),
    observed_at       INTEGER NOT NULL CHECK (observed_at >= 0),
    source_url        TEXT NOT NULL,
    content_hash      BLOB NOT NULL CHECK (length(content_hash) = 8),
    hash_version      INTEGER NOT NULL CHECK (hash_version >= 1),
    payload_encoding  TEXT NOT NULL DEFAULT 'json'
                      CHECK (payload_encoding IN ('json', 'zstd')),
    payload           TEXT NOT NULL,
    PRIMARY KEY (platform, video_id, seq),
    UNIQUE (platform, video_id, observed_at),
    CHECK (payload_encoding <> 'json' OR json_valid(payload)),
    FOREIGN KEY (platform, video_id)
        REFERENCES videos (platform, video_id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE TABLE metric_samples (
    platform      TEXT    NOT NULL CHECK (length(platform) > 0),
    subject_type  TEXT    NOT NULL CHECK (subject_type IN ('creator', 'video')),
    subject_id    TEXT    NOT NULL CHECK (length(subject_id) > 0),
    metric        TEXT    NOT NULL CHECK (length(metric) > 0),
    sampled_at    INTEGER NOT NULL CHECK (sampled_at >= 0),
    value         INTEGER NOT NULL CHECK (value >= 0),
    PRIMARY KEY (platform, subject_type, subject_id, metric, sampled_at)
) WITHOUT ROWID;

CREATE TABLE tracking_targets (
    platform          TEXT    NOT NULL CHECK (length(platform) > 0),
    target_type       TEXT    NOT NULL CHECK (target_type IN ('creator', 'video')),
    target_id         TEXT    NOT NULL CHECK (length(target_id) > 0),
    interval_seconds  INTEGER NOT NULL CHECK (interval_seconds >= 60),
    next_run_at       INTEGER NOT NULL CHECK (next_run_at >= 0),
    last_run_at       INTEGER,
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at        INTEGER NOT NULL CHECK (created_at >= 0),
    PRIMARY KEY (platform, target_type, target_id)
);

CREATE INDEX idx_videos_creator_published
    ON videos (platform, creator_id, published_at DESC, video_id DESC);

CREATE INDEX idx_videos_observation_queue
    ON videos (last_observed_at, platform, video_id);

CREATE INDEX idx_creators_profile_queue
    ON creators (last_profile_observed_at)
    WHERE last_profile_observed_at IS NOT NULL;

CREATE INDEX idx_tracking_due
    ON tracking_targets (enabled, next_run_at);
