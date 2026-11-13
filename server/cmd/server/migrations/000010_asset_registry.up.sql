-- The initial tenant preserves the existing deployment's shared data scope.
-- Additional tenants must not be enabled until all existing data paths are scoped.
CREATE TABLE tenant (
    id UUID PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO tenant (id, name)
VALUES ('00000000-0000-0000-0000-000000000001', '默认租户');

CREATE TABLE tenant_membership (
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
    role VARCHAR(30) NOT NULL CHECK (role IN ('admin', 'analyst', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id)
);

INSERT INTO tenant_membership (tenant_id, user_id, role)
SELECT '00000000-0000-0000-0000-000000000001', id,
       CASE WHEN is_superuser THEN 'admin' ELSE 'analyst' END
FROM auth_user;

CREATE FUNCTION tq_attach_default_tenant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO tenant_membership (tenant_id, user_id, role)
    VALUES ('00000000-0000-0000-0000-000000000001', NEW.id,
            CASE WHEN NEW.is_superuser THEN 'admin' ELSE 'analyst' END)
    ON CONFLICT (tenant_id, user_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tq_auth_user_membership
AFTER INSERT ON auth_user FOR EACH ROW EXECUTE FUNCTION tq_attach_default_tenant();

CREATE TABLE asset_entity (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
    asset_type VARCHAR(20) NOT NULL CHECK (asset_type IN
        ('root_domain', 'subdomain', 'ip', 'app', 'service', 'endpoint')),
    identity_key TEXT COLLATE "C" NOT NULL,
    identity_hash BYTEA NOT NULL CHECK (octet_length(identity_hash) = 32),
    label TEXT NOT NULL CHECK (label <> ''),
    method VARCHAR(20),
    is_manual BOOLEAN NOT NULL DEFAULT FALSE,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(attributes) = 'object'),
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT asset_entity_identity UNIQUE (tenant_id, asset_type, identity_hash),
    CONSTRAINT asset_entity_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT asset_entity_seen_order CHECK (first_seen_at <= last_seen_at),
    CONSTRAINT asset_entity_method_check CHECK (asset_type = 'endpoint' OR method IS NULL)
);
CREATE INDEX idx_asset_entity_tenant_type_id ON asset_entity(tenant_id, asset_type, id DESC);
CREATE INDEX idx_asset_entity_label_trgm ON asset_entity USING GIN (label gin_trgm_ops);

CREATE TABLE asset_subdomain_binding (
    subdomain_id INTEGER PRIMARY KEY REFERENCES subdomain(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT
);
CREATE INDEX idx_asset_subdomain_binding_asset ON asset_subdomain_binding(asset_id);

CREATE TABLE asset_website_binding (
    website_id INTEGER PRIMARY KEY REFERENCES website(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT
);
CREATE INDEX idx_asset_website_binding_asset ON asset_website_binding(asset_id);

CREATE TABLE asset_endpoint_binding (
    endpoint_id INTEGER PRIMARY KEY REFERENCES endpoint(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT
);
CREATE INDEX idx_asset_endpoint_binding_asset ON asset_endpoint_binding(asset_id);

CREATE TABLE asset_host_port_binding (
    host_port_id INTEGER PRIMARY KEY REFERENCES host_port_mapping(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES asset_entity(id) ON DELETE RESTRICT
);
CREATE INDEX idx_asset_host_port_binding_asset ON asset_host_port_binding(asset_id);

-- Existing assets are reconciled in bounded, resumable batches after startup.
CREATE TABLE asset_registry_backfill (
    source_kind VARCHAR(30) PRIMARY KEY,
    last_id BIGINT NOT NULL DEFAULT 0 CHECK (last_id >= 0),
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO asset_registry_backfill (source_kind)
VALUES ('subdomain'), ('website'), ('endpoint'), ('host_port');

-- The serialized identity is owned by PostgreSQL so every caller uses the same
-- byte representation. URL identity is the exact original observation.
CREATE FUNCTION tq_asset_identity(p_type TEXT, p_label TEXT, p_method TEXT)
RETURNS TEXT LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT jsonb_build_array(p_type, p_label, COALESCE(p_method, ''))::text;
$$;

CREATE FUNCTION tq_register_asset(
    p_tenant UUID, p_type TEXT, p_label TEXT, p_method TEXT, p_seen TIMESTAMPTZ
) RETURNS BIGINT LANGUAGE plpgsql AS $$
DECLARE
    v_key TEXT;
    v_id BIGINT;
BEGIN
    IF p_label IS NULL OR p_label = '' OR p_seen IS NULL THEN
        RAISE EXCEPTION 'asset identity and observation time are required';
    END IF;
    v_key := tq_asset_identity(p_type, p_label, p_method);
    INSERT INTO asset_entity
        (tenant_id, asset_type, identity_key, identity_hash, label, method, first_seen_at, last_seen_at)
    VALUES
        (p_tenant, p_type, v_key, sha256(convert_to(v_key, 'UTF8')), p_label, p_method, p_seen, p_seen)
    ON CONFLICT (tenant_id, asset_type, identity_hash) DO UPDATE
        SET first_seen_at = LEAST(asset_entity.first_seen_at, EXCLUDED.first_seen_at),
            last_seen_at = GREATEST(asset_entity.last_seen_at, EXCLUDED.last_seen_at)
        WHERE asset_entity.identity_key = EXCLUDED.identity_key
    RETURNING id INTO v_id;
    IF v_id IS NULL THEN
        RAISE EXCEPTION 'asset identity digest collision';
    END IF;
    RETURN v_id;
END;
$$;

-- Triggers participate in the caller's transaction, including ResultIngest's
-- existing execution fences and snapshot/current-asset materialization.
CREATE FUNCTION tq_bind_legacy_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_asset BIGINT;
    v_tenant CONSTANT UUID := '00000000-0000-0000-0000-000000000001';
BEGIN
    IF TG_TABLE_NAME = 'subdomain' THEN
        v_asset := tq_register_asset(v_tenant, 'subdomain', NEW.dns_name, NULL, NEW.created_at);
        INSERT INTO asset_subdomain_binding(subdomain_id, asset_id) VALUES (NEW.id, v_asset)
            ON CONFLICT (subdomain_id) DO UPDATE SET asset_id = EXCLUDED.asset_id;
    ELSIF TG_TABLE_NAME = 'website' THEN
        v_asset := tq_register_asset(v_tenant, 'service', NEW.url, NULL, NEW.created_at);
        INSERT INTO asset_website_binding(website_id, asset_id) VALUES (NEW.id, v_asset)
            ON CONFLICT (website_id) DO UPDATE SET asset_id = EXCLUDED.asset_id;
    ELSIF TG_TABLE_NAME = 'endpoint' THEN
        -- A LunaFox endpoint does not record an HTTP method; NULL means unknown.
        v_asset := tq_register_asset(v_tenant, 'endpoint', NEW.url, NULL, NEW.created_at);
        INSERT INTO asset_endpoint_binding(endpoint_id, asset_id) VALUES (NEW.id, v_asset)
            ON CONFLICT (endpoint_id) DO UPDATE SET asset_id = EXCLUDED.asset_id;
    ELSIF TG_TABLE_NAME = 'host_port_mapping' THEN
        v_asset := tq_register_asset(v_tenant, 'ip', host(NEW.ip), NULL, NEW.created_at);
        INSERT INTO asset_host_port_binding(host_port_id, asset_id) VALUES (NEW.id, v_asset)
            ON CONFLICT (host_port_id) DO UPDATE SET asset_id = EXCLUDED.asset_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tq_subdomain_asset_binding AFTER INSERT OR UPDATE ON subdomain
FOR EACH ROW EXECUTE FUNCTION tq_bind_legacy_asset();
CREATE TRIGGER tq_website_asset_binding AFTER INSERT OR UPDATE ON website
FOR EACH ROW EXECUTE FUNCTION tq_bind_legacy_asset();
CREATE TRIGGER tq_endpoint_asset_binding AFTER INSERT OR UPDATE ON endpoint
FOR EACH ROW EXECUTE FUNCTION tq_bind_legacy_asset();
CREATE TRIGGER tq_host_port_asset_binding AFTER INSERT OR UPDATE ON host_port_mapping
FOR EACH ROW EXECUTE FUNCTION tq_bind_legacy_asset();
