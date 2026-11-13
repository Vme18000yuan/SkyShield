-- Upgrade the built-in tool contract while preserving administrator prose,
-- defaults, visibility and enabled state. Existing property definitions win
-- except for the widened timeout range supported by the runtime.
UPDATE ai_tool_catalog SET
 schema=jsonb_set(jsonb_set(jsonb_set(schema,'{properties}',
  '{"source_task_ids":{"type":"array","items":{"type":"string"},"maxItems":8,"description":"只读继承的直接来源任务 ID，最多 8 个"},"llm_profile_id":{"type":"string","description":"可选任务模型配置 ID"},"plan_heartbeat_seconds":{"type":"integer","minimum":0,"maximum":2147483647},"seed_first_intent":{"type":"boolean","description":"目标拆解后优先执行一条种子意图"},"timeout_seconds":{"type":"integer","minimum":0,"maximum":2147483647}}'::jsonb || COALESCE(schema->'properties','{}'::jsonb),true),
  '{properties,timeout_seconds,minimum}','0'::jsonb,true),
  '{properties,timeout_seconds,maximum}','2147483647'::jsonb,true),
 description=CASE WHEN description='创建并启动一个独立 AI 任务；可选绑定当前平台目标和已有资产。默认超时 3600 秒。' THEN '创建并启动一个独立 AI 任务；可选绑定平台目标、已有资产及只读来源任务。超时为 0 或不填时不限时。' ELSE description END
WHERE key='spawn_task' AND system=TRUE AND kind='builtin' AND jsonb_typeof(schema->'properties')='object';
