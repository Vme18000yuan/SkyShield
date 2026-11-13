-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TRIGGER tq_anchor_task_asset ON tianqiong_ai.exploration_anchors;
DROP FUNCTION tianqiong_ai.link_anchored_asset();
COMMIT;
