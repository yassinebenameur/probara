package collector

import (
	"context"
	"testing"
)

func TestCollectPopulatesBaselineMetrics(t *testing.T) {
	c := NewCollector("/")

	metrics, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	// CPU cores and uptime should be available on any normal host.
	if metrics.CPUCores <= 0 {
		t.Errorf("expected CPUCores > 0, got %d", metrics.CPUCores)
	}
	if metrics.UptimeSeconds == 0 {
		t.Errorf("expected UptimeSeconds > 0, got %d", metrics.UptimeSeconds)
	}

	// Memory is collected as a critical metric, so it must be populated.
	if metrics.MemoryTotal == 0 {
		t.Errorf("expected MemoryTotal > 0, got %d", metrics.MemoryTotal)
	}

	// At least one real filesystem (the configured "/") should be discovered.
	if len(metrics.DiskMounts) == 0 {
		t.Errorf("expected at least one disk mount, got none")
	}
	for _, m := range metrics.DiskMounts {
		if m.Path == "" {
			t.Errorf("disk mount missing path: %+v", m)
		}
		if m.Total == 0 {
			t.Errorf("disk mount %q has zero total", m.Path)
		}
	}
}

func TestParseDarwinCPUPercent(t *testing.T) {
	topOutput := `
Processes: 498 total, 3 running, 495 sleeping, 2764 threads
Load Avg: 2.82, 3.34, 3.96
CPU usage: 7.21% user, 6.43% sys, 86.34% idle
SharedLibs: 413M resident, 65M data, 56M linkedit.
CPU usage: 17.40% user, 12.00% sys, 70.60% idle
`

	got, err := parseDarwinCPUPercent(topOutput)
	if err != nil {
		t.Fatalf("parseDarwinCPUPercent returned error: %v", err)
	}

	want := 29.4
	if got != want {
		t.Fatalf("parseDarwinCPUPercent = %v, want %v", got, want)
	}
}

func TestParseDarwinCPUPercent_ClampsTo100(t *testing.T) {
	topOutput := `CPU usage: 82.50% user, 30.10% sys, 0.00% idle`

	got, err := parseDarwinCPUPercent(topOutput)
	if err != nil {
		t.Fatalf("parseDarwinCPUPercent returned error: %v", err)
	}
	if got != 100 {
		t.Fatalf("parseDarwinCPUPercent = %v, want 100", got)
	}
}

func TestParseDarwinCPUPercent_InvalidOutput(t *testing.T) {
	_, err := parseDarwinCPUPercent("Load Avg: 1.23, 2.34, 3.45")
	if err == nil {
		t.Fatal("expected error for invalid top output")
	}
}
