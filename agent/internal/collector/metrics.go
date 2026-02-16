package collector

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/yassinebenameur/probara/agent/internal/models"
)

var darwinCPUUsageRe = regexp.MustCompile(`CPU usage:\s*([0-9]+(?:\.[0-9]+)?)% user,\s*([0-9]+(?:\.[0-9]+)?)% sys,\s*([0-9]+(?:\.[0-9]+)?)% idle`)

// Collector collects system metrics
type Collector struct {
	diskPath string
}

// NewCollector creates a new metrics collector
func NewCollector(diskPath string) *Collector {
	if diskPath == "" {
		diskPath = "/"
	}
	return &Collector{
		diskPath: diskPath,
	}
}

// Collect gathers current system metrics
func (c *Collector) Collect(ctx context.Context) (*models.AgentMetrics, error) {
	metrics := &models.AgentMetrics{
		Timestamp: time.Now(),
	}

	// CPU usage (percentage over 1 second interval)
	cpuPercent, err := c.collectCPUPercent(ctx)
	if err != nil {
		if !isNotImplementedError(err) {
			return nil, fmt.Errorf("failed to get CPU usage: %w", err)
		}
		// Keep reporting other metrics when the platform can't provide CPU usage.
		// A negative value is used as a sentinel for "unavailable".
		metrics.CPUPercent = -1
	} else {
		metrics.CPUPercent = cpuPercent
	}

	// Memory metrics
	memInfo, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info: %w", err)
	}
	metrics.MemoryUsed = memInfo.Used
	metrics.MemoryTotal = memInfo.Total

	// Disk metrics
	diskInfo, err := disk.UsageWithContext(ctx, c.diskPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk info: %w", err)
	}
	metrics.DiskUsed = diskInfo.Used
	metrics.DiskTotal = diskInfo.Total

	// Network I/O
	netIO, err := net.IOCountersWithContext(ctx, false)
	if err == nil && len(netIO) > 0 {
		metrics.NetworkBytesIn = netIO[0].BytesRecv
		metrics.NetworkBytesOut = netIO[0].BytesSent
	} else if err != nil {
		return nil, fmt.Errorf("failed to get network I/O: %w", err)
	}

	// Load average
	loadAvg, err := load.AvgWithContext(ctx)
	if err == nil {
		metrics.LoadAvg1 = loadAvg.Load1
		metrics.LoadAvg5 = loadAvg.Load5
		metrics.LoadAvg15 = loadAvg.Load15
	}
	// Load average is not critical, so we don't return error if it fails

	// Process count
	processes, err := process.ProcessesWithContext(ctx)
	if err == nil {
		metrics.ProcessCount = len(processes)
	}
	// Process count is not critical, so we don't return error if it fails

	return metrics, nil
}

func isNotImplementedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not implemented")
}

func (c *Collector) collectCPUPercent(ctx context.Context) (float64, error) {
	cpuPercent, err := cpu.PercentWithContext(ctx, time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		return cpuPercent[0], nil
	}
	if err == nil && len(cpuPercent) == 0 {
		err = fmt.Errorf("empty cpu sample")
	}
	if !isNotImplementedError(err) {
		return 0, err
	}
	if runtime.GOOS == "darwin" {
		return c.collectDarwinCPUPercent(ctx)
	}
	return 0, err
}

func (c *Collector) collectDarwinCPUPercent(ctx context.Context) (float64, error) {
	// For darwin/no-cgo builds, gopsutil CPU collection is not implemented.
	// We parse the final "CPU usage" line from `top -l 2` as an interval sample.
	cmd := exec.CommandContext(ctx, "top", "-l", "2", "-n", "0")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseDarwinCPUPercent(string(out))
}

func parseDarwinCPUPercent(topOutput string) (float64, error) {
	lines := strings.Split(topOutput, "\n")
	lastCPUUsageLine := ""
	for _, line := range lines {
		if strings.Contains(line, "CPU usage:") {
			lastCPUUsageLine = strings.TrimSpace(line)
		}
	}
	if lastCPUUsageLine == "" {
		return 0, fmt.Errorf("cpu usage line not found in top output")
	}

	matches := darwinCPUUsageRe.FindStringSubmatch(lastCPUUsageLine)
	if len(matches) != 4 {
		return 0, fmt.Errorf("unable to parse cpu usage line: %q", lastCPUUsageLine)
	}

	userPercent, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse user cpu percent: %w", err)
	}
	sysPercent, err := strconv.ParseFloat(matches[2], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse system cpu percent: %w", err)
	}

	total := userPercent + sysPercent
	if total < 0 {
		return 0, nil
	}
	if total > 100 {
		return 100, nil
	}
	return total, nil
}
