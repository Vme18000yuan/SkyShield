-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
ALTER TABLE ai_task DROP COLUMN plan_heartbeat_seconds;
ALTER TABLE ai_task DROP CONSTRAINT ai_task_worker_count_check;
ALTER TABLE ai_task DROP CONSTRAINT ai_task_max_turns_check;
ALTER TABLE ai_task DROP CONSTRAINT ai_task_timeout_seconds_check;
UPDATE ai_task SET worker_count=LEAST(16,GREATEST(1,worker_count)),max_turns=GREATEST(1,max_turns),timeout_seconds=LEAST(604800,GREATEST(30,timeout_seconds));
ALTER TABLE ai_task ALTER COLUMN worker_count SET DEFAULT 1;
ALTER TABLE ai_task ALTER COLUMN max_turns SET DEFAULT 30;
ALTER TABLE ai_task ALTER COLUMN timeout_seconds SET DEFAULT 3600;
ALTER TABLE ai_task ADD CONSTRAINT ai_task_worker_count_check CHECK(worker_count BETWEEN 1 AND 16);
ALTER TABLE ai_task ADD CONSTRAINT ai_task_max_turns_check CHECK(max_turns BETWEEN 1 AND 1000);
ALTER TABLE ai_task ADD CONSTRAINT ai_task_timeout_seconds_check CHECK(timeout_seconds BETWEEN 30 AND 604800);
