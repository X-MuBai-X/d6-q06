-- 营销素材 AI 生图场景的数据库结构（SQLite）。
-- 结构是唯一事实来源：服务层不写裸 SQL 之外的表结构假设。

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    role          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    salt          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requirements (
    id                TEXT PRIMARY KEY,
    owner_id          TEXT NOT NULL,
    activity_name     TEXT NOT NULL,
    purpose           TEXT NOT NULL,
    description       TEXT NOT NULL,
    status            TEXT NOT NULL,
    current_task_id   TEXT NOT NULL DEFAULT '',
    current_candidate TEXT NOT NULL DEFAULT '',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_requirements_owner ON requirements(owner_id);
CREATE INDEX IF NOT EXISTS idx_requirements_status ON requirements(status);

CREATE TABLE IF NOT EXISTS generation_tasks (
    id                  TEXT PRIMARY KEY,
    requirement_id      TEXT NOT NULL,
    request_no          TEXT NOT NULL,
    attempt_no          INTEGER NOT NULL,
    description_snapshot TEXT NOT NULL,
    status              TEXT NOT NULL,
    supplier_task_id    TEXT NOT NULL DEFAULT '',
    image_url           TEXT NOT NULL DEFAULT '',
    failure_reason      TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    finished_at         TEXT NOT NULL DEFAULT ''
);

-- 题面 R2：同一需求最多有一个处理中生成任务（部分唯一索引在数据库层兜底）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_one_processing_task
    ON generation_tasks(requirement_id) WHERE status = 'processing';

-- 题面 R2：同请求编号重复发起不创建第二个供应商任务。
-- 幂等键的作用域是「同一需求」：请求编号在需求内唯一，跨需求复用由服务层明确拒绝（409）。
-- DROP INDEX IF EXISTS 用于兼容既有库：旧库里的全局唯一索引被替换为下面的复合唯一索引。
DROP INDEX IF EXISTS idx_task_request_no;
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_requirement_request_no
    ON generation_tasks(requirement_id, request_no);

CREATE INDEX IF NOT EXISTS idx_tasks_requirement ON generation_tasks(requirement_id);
CREATE INDEX IF NOT EXISTS idx_tasks_supplier ON generation_tasks(supplier_task_id);

CREATE TABLE IF NOT EXISTS candidates (
    id             TEXT PRIMARY KEY,
    task_id        TEXT NOT NULL,
    requirement_id TEXT NOT NULL,
    attempt_no     INTEGER NOT NULL,
    image_url      TEXT NOT NULL,
    review_status  TEXT NOT NULL,
    review_reason  TEXT NOT NULL DEFAULT '',
    reviewer_id    TEXT NOT NULL DEFAULT '',
    reviewed_at    TEXT NOT NULL DEFAULT '',
    is_current     INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_candidates_requirement ON candidates(requirement_id);
CREATE INDEX IF NOT EXISTS idx_candidates_task ON candidates(task_id);

CREATE TABLE IF NOT EXISTS events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    requirement_id TEXT NOT NULL DEFAULT '',
    task_id        TEXT NOT NULL DEFAULT '',
    actor_id       TEXT NOT NULL DEFAULT '',
    actor_role     TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL,
    detail         TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_requirement ON events(requirement_id);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);
