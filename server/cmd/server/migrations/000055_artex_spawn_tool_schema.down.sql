-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
-- No schema contraction: preserving customized tool settings is safer than removing fields.
SELECT 1;
