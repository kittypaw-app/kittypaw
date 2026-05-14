CREATE TABLE IF NOT EXISTS conversation_tool_trace_index (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL,
    turn_id INTEGER NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    skill_name TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    success INTEGER NOT NULL DEFAULT 0,
    error_class TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

WITH trace_rows AS (
    SELECT
        t.conversation_id AS conversation_id,
        t.id AS turn_id,
        t.timestamp AS created_at,
        trace.value AS trace_json,
        lower(COALESCE(json_extract(trace.value, '$.error'), '')) AS error_text
    FROM v2_conversation_turns t,
         json_each(
             CASE
                 WHEN t.tool_trace_json IS NULL
                      OR t.tool_trace_json = ''
                      OR NOT json_valid(t.tool_trace_json)
                 THEN '[]'
                 WHEN json_type(t.tool_trace_json) = 'array' THEN t.tool_trace_json
                 ELSE '[]'
             END
         ) AS trace
)
INSERT INTO conversation_tool_trace_index (
    conversation_id, turn_id, trace_id, skill_name, method, success, error_class, created_at
)
SELECT
    conversation_id,
    turn_id,
    COALESCE(json_extract(trace_json, '$.id'), ''),
    COALESCE(json_extract(trace_json, '$.skill_name'), ''),
    COALESCE(json_extract(trace_json, '$.method'), ''),
    CASE WHEN COALESCE(json_extract(trace_json, '$.success'), 0) THEN 1 ELSE 0 END,
    CASE
        WHEN COALESCE(json_extract(trace_json, '$.success'), 0) THEN ''
        WHEN error_text = '' THEN 'failed'
        WHEN instr(error_text, 'permission') > 0
          OR instr(error_text, 'not allowed') > 0
          OR instr(error_text, 'denied') > 0 THEN 'permission'
        WHEN instr(error_text, 'timeout') > 0
          OR instr(error_text, 'deadline') > 0 THEN 'timeout'
        WHEN instr(error_text, 'rate limit') > 0
          OR instr(error_text, '429') > 0 THEN 'rate_limit'
        WHEN instr(error_text, 'not found') > 0 THEN 'not_found'
        ELSE 'error'
    END,
    COALESCE(created_at, datetime('now'))
FROM trace_rows
WHERE NOT EXISTS (
    SELECT 1
    FROM conversation_tool_trace_index existing
    WHERE existing.turn_id = trace_rows.turn_id
      AND existing.trace_id = COALESCE(json_extract(trace_rows.trace_json, '$.id'), '')
);

CREATE INDEX IF NOT EXISTS idx_conversation_tool_trace_conversation
    ON conversation_tool_trace_index(conversation_id, id);

CREATE INDEX IF NOT EXISTS idx_conversation_tool_trace_skill
    ON conversation_tool_trace_index(skill_name, method, success, id);

CREATE INDEX IF NOT EXISTS idx_conversation_tool_trace_error
    ON conversation_tool_trace_index(success, error_class, id);
