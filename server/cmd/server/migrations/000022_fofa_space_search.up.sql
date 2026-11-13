BEGIN;
CREATE TABLE public.fofa_setting (
 tenant_id UUID PRIMARY KEY REFERENCES public.tenant(id) ON DELETE CASCADE,
 email VARCHAR(254) NOT NULL,
 encrypted_api_key BYTEA NOT NULL,
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE public.fofa_search (
 id UUID PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 query TEXT NOT NULL CHECK (octet_length(query) BETWEEN 1 AND 8192),
 page INTEGER NOT NULL CHECK (page BETWEEN 1 AND 1000),
 page_size INTEGER NOT NULL CHECK (page_size BETWEEN 1 AND 100),
 full_history BOOLEAN NOT NULL DEFAULT FALSE,
 total BIGINT NOT NULL CHECK (total>=0),
 records JSONB NOT NULL CHECK (jsonb_typeof(records)='array' AND jsonb_array_length(records)<=100),
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at TIMESTAMPTZ NOT NULL,
 UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE RESTRICT
);
CREATE INDEX idx_fofa_search_expiry ON public.fofa_search(tenant_id,expires_at);
CREATE TABLE public.fofa_import (
 tenant_id UUID NOT NULL,
 search_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 row_index INTEGER NOT NULL CHECK (row_index BETWEEN 0 AND 99),
 organization_id INTEGER NOT NULL REFERENCES public.organization(id) ON DELETE CASCADE,
 requested_target_id INTEGER NOT NULL DEFAULT 0 CHECK (requested_target_id>=0),
 result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(search_id,row_index,organization_id,requested_target_id),
 FOREIGN KEY(tenant_id,search_id) REFERENCES public.fofa_search(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE RESTRICT
);
COMMIT;
