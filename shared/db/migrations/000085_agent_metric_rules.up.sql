-- 000085_agent_metric_rules.up.sql
-- Agent monitors move from the fixed metric_thresholds block
-- (cpu/memory/disk/swap percent, 0-100) to generic metric_rules over the
-- OTel metric store: [{metric_name, attribute_filters, operator, threshold,
-- for_duration_seconds}] with thresholds in the metric's NATIVE unit (ratio
-- 0-1 for *.utilization). Open host_metric alerts are rewritten in place to
-- the new canonical series-key identity so the deploy does not fire a
-- resolve/reopen notification storm. Historical (resolved) alerts keep their
-- legacy metric names; display layers render both.

-- 1) monitors.config: metric_thresholds → metric_rules.
--    cpu_percent    → system.cpu.utilization{state=used}      (ratio)
--    memory_percent → system.memory.utilization{state=used}   (ratio)
--    swap_percent   → system.paging.utilization{state=used}   (ratio)
--    disk_percent   → system.filesystem.utilization{mode=rw}  (fans out per
--                     writable mountpoint — strictly better than the old
--                     single-path aggregate; the mode filter keeps read-only
--                     mounts out, since squashfs/snapshot volumes sit at 100%
--                     forever and would page a migrated fleet instantly)
UPDATE monitors m
SET config = (m.config - 'metric_thresholds') ||
	CASE WHEN rules.arr IS NULL THEN '{}'::jsonb
	     ELSE jsonb_build_object('metric_rules', rules.arr) END
FROM (
	SELECT id,
		(SELECT jsonb_agg(rule) FROM (
			SELECT jsonb_build_object(
				'metric_name', 'system.cpu.utilization',
				'attribute_filters', jsonb_build_object('state', 'used'),
				'operator', '>=',
				'threshold', round((config#>>'{metric_thresholds,cpu_percent}')::numeric / 100, 4)
			) AS rule
			WHERE (config#>>'{metric_thresholds,cpu_percent}')::float8 > 0
			UNION ALL
			SELECT jsonb_build_object(
				'metric_name', 'system.memory.utilization',
				'attribute_filters', jsonb_build_object('state', 'used'),
				'operator', '>=',
				'threshold', round((config#>>'{metric_thresholds,memory_percent}')::numeric / 100, 4)
			)
			WHERE (config#>>'{metric_thresholds,memory_percent}')::float8 > 0
			UNION ALL
			SELECT jsonb_build_object(
				'metric_name', 'system.paging.utilization',
				'attribute_filters', jsonb_build_object('state', 'used'),
				'operator', '>=',
				'threshold', round((config#>>'{metric_thresholds,swap_percent}')::numeric / 100, 4)
			)
			WHERE (config#>>'{metric_thresholds,swap_percent}')::float8 > 0
			UNION ALL
			SELECT jsonb_build_object(
				'metric_name', 'system.filesystem.utilization',
				'attribute_filters', jsonb_build_object('mode', 'rw'),
				'operator', '>=',
				'threshold', round((config#>>'{metric_thresholds,disk_percent}')::numeric / 100, 4)
			)
			WHERE (config#>>'{metric_thresholds,disk_percent}')::float8 > 0
		) r) AS arr
	FROM monitors
	WHERE type = 'agent' AND config ? 'metric_thresholds' AND deleted_at IS NULL
) rules
WHERE m.id = rules.id;

-- Tombstoned agent monitors just lose the legacy block.
UPDATE monitors SET config = config - 'metric_thresholds'
WHERE type = 'agent' AND config ? 'metric_thresholds';

-- 2) Open host_metric alerts: legacy metric names → canonical series keys,
--    percent values → ratios. The aggregate 'disk' key has no per-mount
--    equivalent; it maps to the bare metric name and will resolve once, then
--    reopen per-mount if still breaching (documented, accepted).
UPDATE alerts SET metric_name = 'system.cpu.utilization{state=used}',
	metric_value = metric_value / 100, threshold_value = threshold_value / 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'cpu' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'system.memory.utilization{state=used}',
	metric_value = metric_value / 100, threshold_value = threshold_value / 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'memory' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'system.paging.utilization{state=used}',
	metric_value = metric_value / 100, threshold_value = threshold_value / 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'swap' AND status IN ('active', 'acknowledged');

UPDATE alerts SET metric_name = 'system.filesystem.utilization',
	metric_value = metric_value / 100, threshold_value = threshold_value / 100, updated_at = NOW()
WHERE kind = 'host_metric' AND metric_name = 'disk' AND status IN ('active', 'acknowledged');
