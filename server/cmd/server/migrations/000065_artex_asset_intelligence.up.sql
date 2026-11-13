CREATE INDEX tq_intelligence_asset_ids_gin ON tianqiong_ai.task_intelligence USING gin(asset_ids jsonb_path_ops);
