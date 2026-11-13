-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_template_category_scope ON ai_task_template;
DROP TRIGGER tq_task_category_scope ON ai_task;
DROP FUNCTION tianqiong_ai.guard_task_category_owner();
DROP TABLE ai_task_template;
DROP INDEX ai_task_pinned_owner;
DROP INDEX ai_task_category_owner;
ALTER TABLE ai_task DROP COLUMN pinned_at;
ALTER TABLE ai_task DROP COLUMN remark;
ALTER TABLE ai_task DROP COLUMN category_id;
DROP TABLE ai_task_category;
