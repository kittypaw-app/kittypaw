CREATE TABLE IF NOT EXISTS kanban_projects (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS kanban_boards (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, slug)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kanban_boards_default
    ON kanban_boards(project_id)
    WHERE is_default = 1;

CREATE TABLE IF NOT EXISTS kanban_milestones (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    target_date TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_kanban_milestones_project
    ON kanban_milestones(project_id, status);

CREATE TABLE IF NOT EXISTS kanban_tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    board_id TEXT NOT NULL REFERENCES kanban_boards(id) ON DELETE RESTRICT,
    milestone_id TEXT REFERENCES kanban_milestones(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    assignee TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_project_status
    ON kanban_tasks(project_id, status, priority DESC, created_at);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_board_status
    ON kanban_tasks(board_id, status, priority DESC, created_at);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_milestone
    ON kanban_tasks(milestone_id, status);

CREATE TABLE IF NOT EXISTS kanban_task_links (
    parent_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    child_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL DEFAULT 'blocks',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(parent_id, child_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_links_child
    ON kanban_task_links(child_id, link_type);

CREATE TABLE IF NOT EXISTS kanban_task_comments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    author TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_comments_task
    ON kanban_task_comments(task_id, created_at);

CREATE TABLE IF NOT EXISTS kanban_task_events (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    actor TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_events_task
    ON kanban_task_events(task_id, created_at);

CREATE TABLE IF NOT EXISTS kanban_task_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    actor TEXT NOT NULL DEFAULT '',
    work_dir TEXT NOT NULL,
    work_dir_provider TEXT NOT NULL,
    outcome TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    heartbeat_at TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_runs_task
    ON kanban_task_runs(task_id, started_at);
