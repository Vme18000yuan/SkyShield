ALTER TABLE ai_task ADD COLUMN bootstrap_complete BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE ai_task task SET bootstrap_complete=TRUE WHERE task.first_run_at IS NOT NULL OR EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes n WHERE n.exploration_id=task.exploration_id AND n.kind='goal');
