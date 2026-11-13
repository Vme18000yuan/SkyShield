-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_main_session ON tianqiong_ai.main_sessions;
DROP FUNCTION tianqiong_ai.guard_main_session();
