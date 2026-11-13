-- DESTRUCTIVE TEST TEARDOWN ONLY. Not a production data-recovery procedure.
CREATE OR REPLACE FUNCTION tq_inventory_host_port(raw text) RETURNS text
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    authority text;
    parts text[];
    host_name text;
    port_number integer;
    default_port integer;
BEGIN
    IF raw ~* '^https?://' THEN
        default_port := CASE WHEN raw ~* '^https://' THEN 443 ELSE 80 END;
        authority := regexp_replace(raw, '^https?://', '', 'i');
        authority := regexp_replace(authority, '[/\?#].*$', '');
        authority := regexp_replace(authority, '^.*@', '');
    ELSE
        authority := raw;
    END IF;
    parts := regexp_match(authority, '^(\[[0-9a-fA-F:.]+\]|[^[:space:]:/?#@]+)(:([0-9]{1,5}))?$');
    IF parts IS NULL THEN RETURN NULL; END IF;
    host_name := lower(rtrim(parts[1], '.'));
    IF host_name = '' THEN RETURN NULL; END IF;
    port_number := COALESCE(NULLIF(parts[3], '')::integer, default_port);
    IF port_number IS NULL OR port_number < 1 OR port_number > 65535 THEN RETURN NULL; END IF;
    RETURN host_name || ':' || port_number::text;
END;
$$;
