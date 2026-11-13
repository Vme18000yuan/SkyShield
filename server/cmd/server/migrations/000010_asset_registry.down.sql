-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
DROP TRIGGER IF EXISTS tq_host_port_asset_binding ON host_port_mapping;
DROP TRIGGER IF EXISTS tq_endpoint_asset_binding ON endpoint;
DROP TRIGGER IF EXISTS tq_website_asset_binding ON website;
DROP TRIGGER IF EXISTS tq_subdomain_asset_binding ON subdomain;
DROP FUNCTION IF EXISTS tq_bind_legacy_asset();
DROP FUNCTION IF EXISTS tq_register_asset(UUID, TEXT, TEXT, TEXT, TIMESTAMPTZ);
DROP FUNCTION IF EXISTS tq_asset_identity(TEXT, TEXT, TEXT);
DROP TABLE IF EXISTS asset_registry_backfill;
DROP TABLE IF EXISTS asset_host_port_binding;
DROP TABLE IF EXISTS asset_endpoint_binding;
DROP TABLE IF EXISTS asset_website_binding;
DROP TABLE IF EXISTS asset_subdomain_binding;
DROP TABLE IF EXISTS asset_entity;
DROP TRIGGER IF EXISTS tq_auth_user_membership ON auth_user;
DROP FUNCTION IF EXISTS tq_attach_default_tenant();
DROP TABLE IF EXISTS tenant_membership;
DROP TABLE IF EXISTS tenant;
