-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_graph_finding_fence ON tianqiong_ai.findings;
CREATE TRIGGER tq_graph_finding_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.findings FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP FUNCTION tianqiong_ai.guard_finding_write();
DROP TABLE ai_tool_mutation_receipt;
