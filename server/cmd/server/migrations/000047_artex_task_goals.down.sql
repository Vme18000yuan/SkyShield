-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_human_task_event ON ai_task_human_event;
DROP FUNCTION tianqiong_ai.guard_human_task_event();
DROP TABLE ai_task_human_event;
DROP TRIGGER tq_graph_intelligence_fence ON tianqiong_ai.task_intelligence;
CREATE TRIGGER tq_graph_intelligence_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.task_intelligence FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP TRIGGER tq_graph_activity_fence ON tianqiong_ai.activity;
CREATE TRIGGER tq_graph_activity_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.activity FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP TRIGGER tq_graph_anchor_fence ON tianqiong_ai.exploration_anchors;
CREATE TRIGGER tq_graph_anchor_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_anchors FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP FUNCTION tianqiong_ai.guard_goal_dependent_write();
DROP TRIGGER tq_graph_edge_fence ON tianqiong_ai.exploration_edges;
CREATE TRIGGER tq_graph_edge_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_edges FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP FUNCTION tianqiong_ai.guard_goal_edge_write();
DROP TRIGGER tq_graph_node_fence ON tianqiong_ai.exploration_nodes;
CREATE TRIGGER tq_graph_node_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_nodes FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_graph_write();
DROP FUNCTION tianqiong_ai.guard_goal_node_write();
DROP FUNCTION tianqiong_ai.assert_human_goal_scope(BIGINT);
