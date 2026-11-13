-- DESTRUCTIVE TEST TEARDOWN ONLY. Not a production data-recovery procedure.
DROP VIEW IF EXISTS asset_inventory_visible_source;
DROP VIEW IF EXISTS asset_inventory_source;
DROP FUNCTION IF EXISTS tq_inventory_host_port(text);
