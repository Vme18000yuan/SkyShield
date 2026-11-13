-- DESTRUCTIVE TEST TEARDOWN ONLY.
DROP TRIGGER IF EXISTS ai_finding_case_content_changed ON tianqiong_ai.findings;
DROP TABLE IF EXISTS tianqiong_ai.finding_case_suggestion_members CASCADE;
DROP TABLE IF EXISTS tianqiong_ai.finding_case_suggestions CASCADE;
DROP TABLE IF EXISTS tianqiong_ai.finding_case_blocks CASCADE;
DROP TABLE IF EXISTS tianqiong_ai.finding_case_events CASCADE;
DROP TABLE IF EXISTS tianqiong_ai.finding_case_members CASCADE;
DROP TABLE IF EXISTS tianqiong_ai.finding_cases CASCADE;
DROP FUNCTION IF EXISTS tianqiong_ai.finding_case_member_removed();
DROP FUNCTION IF EXISTS tianqiong_ai.finding_case_suggestion_member_removed();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_suggestion_member_write();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_suggestion_write();
DROP FUNCTION IF EXISTS tianqiong_ai.finding_case_finding_changed();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_block_write();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_event_write();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_member_write();
DROP FUNCTION IF EXISTS tianqiong_ai.guard_finding_case_write();
DROP FUNCTION IF EXISTS tianqiong_ai.assert_finding_case_scope();
