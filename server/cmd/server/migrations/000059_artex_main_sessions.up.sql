CREATE FUNCTION tianqiong_ai.guard_main_session() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp bigint; v_task public.ai_task%ROWTYPE; v_next integer;
BEGIN
 IF TG_OP='DELETE' THEN
  IF pg_trigger_depth()>1 AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.explorations WHERE id=OLD.exploration_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'AI main session deletion rejected';
 END IF;
 v_exp:=NEW.exploration_id;
 IF TG_OP<>'INSERT' OR current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'human_main_session' THEN RAISE EXCEPTION 'AI main session write rejected'; END IF;
 v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
 SELECT COALESCE(MAX(seq),0)+1 INTO v_next FROM tianqiong_ai.main_sessions WHERE exploration_id=v_exp;
 IF NEW.seq<>v_next OR NEW.seq<1 THEN RAISE EXCEPTION 'AI main session sequence rejected'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_guard_main_session BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.main_sessions FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_main_session();
