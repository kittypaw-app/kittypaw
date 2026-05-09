CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active', 'archived')),
    next_ticket_seq INTEGER NOT NULL DEFAULT 1,
    project_conversation_id TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    archived_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_projects_state_updated
    ON projects(state, updated_at);

CREATE TABLE IF NOT EXISTS project_staff_assignments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    staff_id TEXT NOT NULL,
    role TEXT NOT NULL,
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, staff_id, role)
);

CREATE INDEX IF NOT EXISTS idx_project_staff_assignments_project
    ON project_staff_assignments(project_id, role, is_primary);

CREATE TABLE IF NOT EXISTS project_driver_settings (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    default_driver_id TEXT NOT NULL DEFAULT 'codex',
    default_mode TEXT NOT NULL DEFAULT 'one_shot',
    default_worktree_policy TEXT NOT NULL DEFAULT 'preserve',
    autonomy_policy TEXT NOT NULL DEFAULT 'edit_and_test',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tickets (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'backlog' CHECK(status IN ('draft', 'backlog', 'ready', 'in_progress', 'blocked', 'review', 'done', 'archived')),
    priority INTEGER NOT NULL DEFAULT 0,
    labels_json TEXT NOT NULL DEFAULT '[]',
    ticket_conversation_id TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    archived_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_tickets_project_status
    ON tickets(project_id, status, priority DESC, created_at);

CREATE TABLE IF NOT EXISTS ticket_dependencies (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    blocker_ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    blocked_ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    type TEXT NOT NULL DEFAULT 'blocks' CHECK(type IN ('blocks', 'relates_to', 'duplicates')),
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(blocker_ticket_id, blocked_ticket_id, type)
);

CREATE INDEX IF NOT EXISTS idx_ticket_dependencies_blocked
    ON ticket_dependencies(blocked_ticket_id, type);

CREATE TABLE IF NOT EXISTS ticket_actions (
    id TEXT PRIMARY KEY,
    ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL DEFAULT '',
    action_type TEXT NOT NULL,
    from_status TEXT NOT NULL DEFAULT '',
    to_status TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ticket_actions_ticket
    ON ticket_actions(ticket_id, created_at);

CREATE TABLE IF NOT EXISTS ticket_messages (
    id TEXT PRIMARY KEY,
    ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL DEFAULT '',
    author_id TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket
    ON ticket_messages(ticket_id, created_at);

CREATE TABLE IF NOT EXISTS ticket_staff_assignments (
    id TEXT PRIMARY KEY,
    ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    staff_id TEXT NOT NULL,
    role TEXT NOT NULL,
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(ticket_id, staff_id, role)
);

CREATE INDEX IF NOT EXISTS idx_ticket_staff_assignments_ticket
    ON ticket_staff_assignments(ticket_id, role, is_primary);

CREATE TABLE IF NOT EXISTS project_brief_drafts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft', 'committed', 'discarded')),
    title TEXT NOT NULL DEFAULT '',
    brief_json TEXT NOT NULL DEFAULT '{}',
    proposed_tickets_json TEXT NOT NULL DEFAULT '[]',
    created_by TEXT NOT NULL DEFAULT '',
    committed_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_project_brief_drafts_project
    ON project_brief_drafts(project_id, status, created_at);

CREATE TABLE IF NOT EXISTS driver_definitions (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    command TEXT NOT NULL,
    supported_modes_json TEXT NOT NULL DEFAULT '[]',
    default_args_json TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    driver_id TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'one_shot' CHECK(mode IN ('one_shot', 'pty', 'tmux')),
    status TEXT NOT NULL DEFAULT 'planned' CHECK(status IN ('planned', 'approved', 'running', 'succeeded', 'failed', 'canceled')),
    worktree_path TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    prompt_summary TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL DEFAULT '',
    result_summary TEXT NOT NULL DEFAULT '',
    log_tail TEXT NOT NULL DEFAULT '',
    error_excerpt TEXT NOT NULL DEFAULT '',
    log_truncated INTEGER NOT NULL DEFAULT 0,
    driver_snapshot_json TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT '',
    approved_by TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_ticket_status
    ON jobs(ticket_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_jobs_project_status
    ON jobs(project_id, status, created_at);

CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_job_events_job
    ON job_events(job_id, created_at);

CREATE TABLE IF NOT EXISTS conversation_scope (
    conversation_id TEXT PRIMARY KEY,
    scope_type TEXT NOT NULL CHECK(scope_type IN ('general', 'project', 'ticket')),
    scope_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
