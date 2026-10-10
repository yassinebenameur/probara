-- Best-effort reverse: rules matching the four canonical migrations map back
-- to metric_thresholds (ratio → percent); any other rules are dropped.
UPDATE monitors m
SET config = (m.config - 'metric_rules') ||
	jsonb_build_object('metric_thresholds', jsonb_strip_nulls(jsonb_build_object(
		'cpu_percent', (
			SELECT round((r->>'threshold')::numeric * 100, 2)
			FROM jsonb_array_elements(m.config->'metric_rules') r
			WHERE r->>'metric_name' = 'system.cpu.utilization' LIMIT 1),
		'memory_percent', (
			SELECT round((r->>'threshold')::numeric * 100, 2)
			FROM jsonb_array_elements(m.config->'metric_rules') r
			WHERE r->>'metric_name' = 'system.memory.utilization' LIMIT 1),
		'swap_percent', (
			SELECT round((r->>'threshold')::numeric * 100, 2)
			FROM jsonb_array_elements(m.config->'metric_rules') r
			WHERE r->>'metric_name' = 'system.paging.utilization' LIMIT 1),
		'disk_percent', (
			SELECT round((r->>'threshold')::numeric * 100, 2)
			FROM jsonb_array_elements(m.config->'metric_rules') r
			WHERE r->>'metric_name' = 'system.filesystem.utilization' LIMIT 1)
	)))
WHERE m.type = 'agent' AND m.config ? 'metric_rules';

UPDATE alerts SET metric_name = 'cpu',
	metric_value = metric_value * 100, threshold_value = threshold_value * 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'system.cpu.utilization{state=used}' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'memory',
	metric_value = metric_value * 100, threshold_value = threshold_value * 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'system.memory.utilization{state=used}' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'swap',
	metric_value = metric_value * 100, threshold_value = threshold_value * 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'system.paging.utilization{state=used}' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'disk',
	metric_value = metric_value * 100, threshold_value = threshold_value * 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'system.filesystem.utilization' AND status IN ('active', 'acknowledged');
