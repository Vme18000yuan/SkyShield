-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
DROP FUNCTION IF EXISTS tq_register_asset_v2(UUID,TEXT,TEXT,TEXT,TEXT,TEXT,TIMESTAMPTZ);
DROP TABLE IF EXISTS asset_subdomain_variant_binding;
DROP TABLE IF EXISTS asset_endpoint_variant_binding;
DROP TABLE IF EXISTS asset_observation;
ALTER TABLE asset_entity DROP CONSTRAINT IF EXISTS asset_entity_record_type_check;
ALTER TABLE asset_entity DROP COLUMN IF EXISTS record_type;
