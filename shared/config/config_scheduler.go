// Scheduler service configuration.

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SchedulerConfig contains configuration for the scheduler service
type SchedulerConfig struct {
	BaseConfig
	ScheduleIntervalSeconds int
	SchedulerBatchSize      int
	CheckJobSubject         string
	CheckJobStream          string
	// Results ingest: the scheduler hosts the consumer that persists check
	// results published by workers over NATS (workers have no DB access).
	CheckResultStream        string
	CheckResultSubject       string
	ResultIngestConsumerName string
	ResultIngestConcurrency  int
	ResultIngestEnabled      bool
	// LegacyCheckConsumers are pre-locations filterless consumer names the
	// scheduler deletes on startup: a work-queue stream can't host both a
	// filterless consumer and the filtered per-location ones.
	LegacyCheckConsumers          []string
	RetentionCleanupEnabled       bool
	RetentionCleanupHourUTC       int
	RetentionCleanupBatchSize     int
	RetentionCleanupMaxRowsPerRun int
	// Monitor purge: removes child rows of soft-deleted monitors then the row itself.
	MonitorPurgeEnabled         bool
	MonitorPurgeIntervalSeconds int
	MonitorPurgeBatchSize       int
	MonitorPurgeMaxRowsPerRun   int
	// Mesh: inter-location connectivity probing. Edges are derived from
	// locations with a mesh_endpoint set; the threshold is platform-level for
	// now because an edge has no monitor row to carry one.
	MeshEnabled              bool
	MeshProbeIntervalSeconds int
	MeshProbeTimeoutSeconds  int
	MeshFailureThreshold     int
	MeshScheduleBatchSize    int
	// MetricRawRetentionDays is how long raw metric_samples partitions are
	// kept before the partition-maintenance pass drops them (hourly metric
	// rollups survive independently). Per-tenant data_retention_days can only
	// tighten this, never extend it.
	MetricRawRetentionDays int
}

// LoadSchedulerConfig loads scheduler service configuration
func LoadSchedulerConfig() (*SchedulerConfig, error) {
	base, err := LoadBaseConfig("scheduler")
	if err != nil {
		return nil, err
	}

	cfg := &SchedulerConfig{BaseConfig: *base}

	// SCHEDULE_INTERVAL_SECONDS
	scheduleIntervalStr := os.Getenv("SCHEDULE_INTERVAL_SECONDS")
	if scheduleIntervalStr == "" {
		cfg.ScheduleIntervalSeconds = 2
	} else {
		scheduleInterval, err := strconv.Atoi(scheduleIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SCHEDULE_INTERVAL_SECONDS: %w", err)
		}
		cfg.ScheduleIntervalSeconds = scheduleInterval
	}

	// SCHEDULER_BATCH_SIZE
	batchSizeStr := os.Getenv("SCHEDULER_BATCH_SIZE")
	if batchSizeStr == "" {
		cfg.SchedulerBatchSize = 500
	} else {
		batchSize, err := strconv.Atoi(batchSizeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SCHEDULER_BATCH_SIZE: %w", err)
		}
		cfg.SchedulerBatchSize = batchSize
	}

	queues := loadCheckQueueConfig()
	cfg.CheckJobSubject = queues.JobSubject
	cfg.CheckJobStream = queues.JobStream

	// Results ingest — the scheduler-side consumer persisting worker results.
	cfg.CheckResultStream = queues.ResultStream
	cfg.CheckResultSubject = queues.ResultSubject
	cfg.ResultIngestConsumerName = envOrDefault("RESULT_INGEST_CONSUMER_NAME", "result-ingest")
	cfg.ResultIngestConcurrency = 10
	if v := strings.TrimSpace(os.Getenv("RESULT_INGEST_CONCURRENCY")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid RESULT_INGEST_CONCURRENCY: %q", v)
		}
		cfg.ResultIngestConcurrency = n
	}
	cfg.ResultIngestEnabled = true
	if v := strings.TrimSpace(os.Getenv("RESULT_INGEST_ENABLED")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid RESULT_INGEST_ENABLED: %w", err)
		}
		cfg.ResultIngestEnabled = enabled
	}

	// CHECK_JOB_LEGACY_CONSUMERS — comma-separated filterless consumer names
	// to delete on startup. Defaults cover the code default and the compose
	// value used before per-location consumers existed.
	legacyConsumers := envOrDefault("CHECK_JOB_LEGACY_CONSUMERS", "check-workers,worker")
	for _, name := range strings.Split(legacyConsumers, ",") {
		if name = strings.TrimSpace(name); name != "" {
			cfg.LegacyCheckConsumers = append(cfg.LegacyCheckConsumers, name)
		}
	}

	// RETENTION_CLEANUP_ENABLED
	retentionCleanupEnabledStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_ENABLED"))
	if retentionCleanupEnabledStr == "" {
		cfg.RetentionCleanupEnabled = true
	} else {
		enabled, err := strconv.ParseBool(retentionCleanupEnabledStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_ENABLED: %w", err)
		}
		cfg.RetentionCleanupEnabled = enabled
	}

	// RETENTION_CLEANUP_HOUR_UTC
	retentionCleanupHourUTCStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_HOUR_UTC"))
	if retentionCleanupHourUTCStr == "" {
		cfg.RetentionCleanupHourUTC = 2
	} else {
		hour, err := strconv.Atoi(retentionCleanupHourUTCStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_HOUR_UTC: %w", err)
		}
		if hour < 0 || hour > 23 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_HOUR_UTC: must be between 0 and 23")
		}
		cfg.RetentionCleanupHourUTC = hour
	}

	// RETENTION_CLEANUP_BATCH_SIZE
	retentionCleanupBatchSizeStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_BATCH_SIZE"))
	if retentionCleanupBatchSizeStr == "" {
		cfg.RetentionCleanupBatchSize = 5000
	} else {
		size, err := strconv.Atoi(retentionCleanupBatchSizeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_BATCH_SIZE: %w", err)
		}
		if size <= 0 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_BATCH_SIZE: must be greater than 0")
		}
		cfg.RetentionCleanupBatchSize = size
	}

	// RETENTION_CLEANUP_MAX_ROWS_PER_RUN
	retentionCleanupMaxRowsStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_MAX_ROWS_PER_RUN"))
	if retentionCleanupMaxRowsStr == "" {
		cfg.RetentionCleanupMaxRowsPerRun = 200000
	} else {
		maxRows, err := strconv.Atoi(retentionCleanupMaxRowsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_MAX_ROWS_PER_RUN: %w", err)
		}
		if maxRows <= 0 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_MAX_ROWS_PER_RUN: must be greater than 0")
		}
		cfg.RetentionCleanupMaxRowsPerRun = maxRows
	}

	cfg.MonitorPurgeEnabled = true
	if v := os.Getenv("MONITOR_PURGE_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_ENABLED: %w", err)
		}
		cfg.MonitorPurgeEnabled = enabled
	}

	cfg.MonitorPurgeIntervalSeconds = 30
	if v := os.Getenv("MONITOR_PURGE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_INTERVAL_SECONDS: %q", v)
		}
		cfg.MonitorPurgeIntervalSeconds = n
	}

	cfg.MonitorPurgeBatchSize = 5000
	if v := os.Getenv("MONITOR_PURGE_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_BATCH_SIZE: %q", v)
		}
		cfg.MonitorPurgeBatchSize = n
	}

	cfg.MonitorPurgeMaxRowsPerRun = 200000
	if v := os.Getenv("MONITOR_PURGE_MAX_ROWS_PER_RUN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_MAX_ROWS_PER_RUN: %q", v)
		}
		cfg.MonitorPurgeMaxRowsPerRun = n
	}

	cfg.MeshEnabled = true
	if v := strings.TrimSpace(os.Getenv("MESH_ENABLED")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MESH_ENABLED: %w", err)
		}
		cfg.MeshEnabled = enabled
	}

	cfg.MeshProbeIntervalSeconds = 30
	if v := os.Getenv("MESH_PROBE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_PROBE_INTERVAL_SECONDS: %q", v)
		}
		cfg.MeshProbeIntervalSeconds = n
	}

	cfg.MeshProbeTimeoutSeconds = 5
	if v := os.Getenv("MESH_PROBE_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_PROBE_TIMEOUT_SECONDS: %q", v)
		}
		cfg.MeshProbeTimeoutSeconds = n
	}

	cfg.MeshFailureThreshold = 3
	if v := os.Getenv("MESH_FAILURE_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_FAILURE_THRESHOLD: %q", v)
		}
		cfg.MeshFailureThreshold = n
	}

	cfg.MeshScheduleBatchSize = 500
	if v := os.Getenv("MESH_SCHEDULE_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_SCHEDULE_BATCH_SIZE: %q", v)
		}
		cfg.MeshScheduleBatchSize = n
	}

	cfg.MetricRawRetentionDays = 30
	if v := strings.TrimSpace(os.Getenv("METRIC_RAW_RETENTION_DAYS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid METRIC_RAW_RETENTION_DAYS: %q", v)
		}
		cfg.MetricRawRetentionDays = n
	}

	return cfg, nil
}
