ALTER TABLE conversation_state ADD COLUMN conversation_staff_id TEXT NOT NULL DEFAULT '';

UPDATE conversation_state
SET conversation_staff_id = COALESCE((
    SELECT value
    FROM user_context
    WHERE key LIKE 'active_staff:%'
      AND value != ''
    ORDER BY updated_at DESC
    LIMIT 1
), '')
WHERE id = 1
  AND conversation_staff_id = '';
