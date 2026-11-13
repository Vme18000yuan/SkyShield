-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_graph_constraint_fence ON tianqiong_ai.task_constraints;
CREATE TRIGGER tq_graph_constraint_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.task_constraints FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP FUNCTION tianqiong_ai.guard_task_constraint_write();
