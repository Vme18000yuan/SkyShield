-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup or a forward fix.
DROP TRIGGER tq_ai_tool_agent_event ON tianqiong_ai.activity;
DROP TRIGGER tq_ai_node_agent_event ON tianqiong_ai.exploration_nodes;
DROP TRIGGER tq_ai_task_agent_event ON ai_task;
DROP FUNCTION tq_capture_ai_agent_event();
DROP TABLE ai_agent_event;
DROP TABLE ai_agent_event_cursor;
DROP TABLE ai_agent_trigger_job;
DROP TABLE ai_agent_trigger;
DROP TABLE ai_runtime_seed;
DROP TABLE ai_agent_option;
ALTER TABLE ai_custom_agent DROP COLUMN preset_key;
