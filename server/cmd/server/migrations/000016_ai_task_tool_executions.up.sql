-- Preserve run attribution for task activity without guessing old records.
BEGIN;
ALTER TABLE tianqiong_ai.activity ADD COLUMN execution_id UUID;
CREATE FUNCTION tianqiong_ai.assign_activity_execution() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
   NEW.execution_id:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
   IF NEW.execution_id IS NULL THEN RAISE EXCEPTION 'AI task activity requires an execution identity'; END IF;
 ELSIF NEW.execution_id IS DISTINCT FROM OLD.execution_id THEN
   RAISE EXCEPTION 'AI task activity execution identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_activity_execution BEFORE INSERT OR UPDATE ON tianqiong_ai.activity
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.assign_activity_execution();
CREATE INDEX idx_tq_activity_execution_tool ON tianqiong_ai.activity(exploration_id,execution_id,worker,node_id,tool_use_id,id);
COMMIT;
