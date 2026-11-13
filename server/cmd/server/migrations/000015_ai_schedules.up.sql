-- ARTEX calendar window semantics adapted to Tianqiong task ownership.
BEGIN;
CREATE TABLE public.ai_schedule (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 name VARCHAR(200) NOT NULL CHECK(btrim(name)<>''),
 remark TEXT NOT NULL DEFAULT '',
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 timezone_mode VARCHAR(10) NOT NULL CHECK(timezone_mode IN ('beijing','system')),
 schedule_type VARCHAR(10) NOT NULL CHECK(schedule_type IN ('once','weekly')),
 run_date DATE,
 end_date DATE,
 weekdays JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(weekdays)='array'),
 start_time TIME NOT NULL,
 end_time TIME,
 start_immediately BOOLEAN NOT NULL DEFAULT FALSE,
 status VARCHAR(16) NOT NULL DEFAULT 'waiting' CHECK(status IN ('waiting','running','outside','completed','error','paused')),
 last_window_key TEXT NOT NULL DEFAULT '',
 last_transition_at TIMESTAMPTZ,
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE RESTRICT,
 CHECK ((schedule_type='once' AND run_date IS NOT NULL) OR (schedule_type='weekly' AND run_date IS NULL))
);
CREATE INDEX idx_ai_schedule_owner ON public.ai_schedule(tenant_id,owner_user_id,id DESC);
CREATE TABLE public.ai_schedule_task (
 schedule_id BIGINT NOT NULL REFERENCES public.ai_schedule(id) ON DELETE CASCADE,
 task_id BIGINT NOT NULL REFERENCES public.ai_task(id) ON DELETE CASCADE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(schedule_id,task_id)
);
CREATE INDEX idx_ai_schedule_task_task ON public.ai_schedule_task(task_id,schedule_id);
CREATE TABLE public.ai_schedule_history (
 id BIGSERIAL PRIMARY KEY,
 schedule_id BIGINT NOT NULL REFERENCES public.ai_schedule(id) ON DELETE CASCADE,
 task_id BIGINT REFERENCES public.ai_task(id) ON DELETE SET NULL,
 action TEXT NOT NULL,
 success BOOLEAN NOT NULL DEFAULT TRUE,
 message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_ai_schedule_history ON public.ai_schedule_history(schedule_id,id DESC);
COMMIT;
