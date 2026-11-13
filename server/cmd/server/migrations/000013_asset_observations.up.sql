ALTER TABLE asset_entity ADD COLUMN record_type VARCHAR(30);
ALTER TABLE asset_entity ADD CONSTRAINT asset_entity_record_type_check
    CHECK (record_type IS NULL OR asset_type='subdomain');

CREATE TABLE asset_observation (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    asset_id BIGINT NOT NULL,
    target_id INTEGER REFERENCES target(id) ON DELETE SET NULL,
    original_target_id INTEGER,
    organization_id INTEGER REFERENCES organization(id) ON DELETE SET NULL,
    created_by INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
    origin VARCHAR(20) NOT NULL CHECK (origin IN ('manual','ai','import')),
    source_key TEXT NOT NULL,
    request_fingerprint VARCHAR(100) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(payload)='object'),
    encrypted_auth BYTEA,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT asset_observation_entity_fk FOREIGN KEY (tenant_id,asset_id)
        REFERENCES asset_entity(tenant_id,id) ON DELETE RESTRICT,
    CONSTRAINT asset_observation_request_unique UNIQUE (tenant_id,origin,source_key)
);
CREATE INDEX idx_asset_observation_asset ON asset_observation(tenant_id,asset_id,id);
CREATE INDEX idx_asset_observation_target ON asset_observation(target_id,asset_id) WHERE target_id IS NOT NULL;

CREATE TABLE asset_endpoint_variant_binding (
    endpoint_id INTEGER NOT NULL REFERENCES endpoint(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT,
    PRIMARY KEY (endpoint_id,asset_id)
);
CREATE INDEX idx_asset_endpoint_variant_asset ON asset_endpoint_variant_binding(asset_id);

CREATE TABLE asset_subdomain_variant_binding (
    subdomain_id INTEGER NOT NULL REFERENCES subdomain(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT,
    PRIMARY KEY (subdomain_id,asset_id)
);
CREATE INDEX idx_asset_subdomain_variant_asset ON asset_subdomain_variant_binding(asset_id);

CREATE FUNCTION tq_register_asset_v2(
    p_tenant UUID,p_type TEXT,p_identity TEXT,p_label TEXT,p_method TEXT,p_record_type TEXT,p_seen TIMESTAMPTZ
) RETURNS BIGINT LANGUAGE plpgsql AS $$
DECLARE v_id BIGINT;
BEGIN
    IF p_identity IS NULL OR p_label IS NULL OR p_label='' OR p_seen IS NULL THEN
        RAISE EXCEPTION 'asset identity and observation time are required';
    END IF;
    INSERT INTO asset_entity(tenant_id,asset_type,identity_key,identity_hash,label,method,record_type,first_seen_at,last_seen_at)
    VALUES(p_tenant,p_type,p_identity,sha256(convert_to(p_identity,'UTF8')),p_label,p_method,p_record_type,p_seen,p_seen)
    ON CONFLICT(tenant_id,asset_type,identity_hash) DO UPDATE
        SET first_seen_at=LEAST(asset_entity.first_seen_at,EXCLUDED.first_seen_at),
            last_seen_at=GREATEST(asset_entity.last_seen_at,EXCLUDED.last_seen_at)
        WHERE asset_entity.identity_key=EXCLUDED.identity_key
    RETURNING id INTO v_id;
    IF v_id IS NULL THEN RAISE EXCEPTION 'asset identity digest collision'; END IF;
    RETURN v_id;
END;
$$;
