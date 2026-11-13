BEGIN;
ALTER TABLE asset_monitor_ip_location
  ADD COLUMN country_code TEXT NOT NULL DEFAULT '',
  ADD COLUMN region_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN city_name TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT asset_monitor_city_labels CHECK (
    (country_code = '' OR country_code ~ '^[A-Z]{2}$')
    AND char_length(region_name) <= 128
    AND char_length(city_name) <= 128
  );
-- Keep existing coordinates usable and admit one gradual metadata refresh.
UPDATE asset_monitor_ip_location
SET next_attempt_at = LEAST(next_attempt_at, CURRENT_TIMESTAMP)
WHERE resolved_at IS NOT NULL;
COMMIT;
