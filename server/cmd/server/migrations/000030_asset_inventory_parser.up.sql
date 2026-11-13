-- Use scalar parsing in this hot path. This avoids retaining a regexp_match
-- array while PostgreSQL evaluates the projection repeatedly in one session.
CREATE OR REPLACE FUNCTION tq_inventory_host_port(raw text) RETURNS text
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    authority text;
    host_name text;
    port_text text;
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
    IF authority !~ '^(\[[0-9a-fA-F:.]+\]|[^[:space:]:/?#@]+)(:[0-9]{1,5})?$' THEN RETURN NULL; END IF;
    port_text := substring(authority FROM ':([0-9]{1,5})$');
    host_name := CASE WHEN port_text IS NULL THEN authority ELSE left(authority, length(authority)-length(port_text)-1) END;
    host_name := lower(rtrim(host_name, '.'));
    IF host_name = '' THEN RETURN NULL; END IF;
    port_number := COALESCE(port_text::integer, default_port);
    IF port_number IS NULL OR port_number < 1 OR port_number > 65535 THEN RETURN NULL; END IF;
    RETURN host_name || ':' || port_number::text;
END;
$$;
