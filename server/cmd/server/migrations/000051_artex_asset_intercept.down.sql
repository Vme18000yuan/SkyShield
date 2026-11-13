-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_asset_intercept_owner ON ai_asset_intercept_rule;
DROP FUNCTION tianqiong_ai.guard_asset_intercept_owner();
DROP TABLE ai_asset_intercept_rule;
