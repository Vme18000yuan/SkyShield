BEGIN;
-- Keep the binding after Agent deletion so a lost identity is not silently
-- replaced on the next local start.
CREATE TABLE public.deployment_local_agent_bootstrap (
 singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
 instance_id TEXT NOT NULL CHECK (instance_id <> '')
);
COMMIT;
