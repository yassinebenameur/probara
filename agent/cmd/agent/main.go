package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/agent/internal/collector"
	"github.com/yassinebenameur/probara/agent/internal/reporter"
)

func main() {
	// Command-line flags
	backendURL := flag.String("backend-url", "", "Backend API URL (required)")
	agentID := flag.String("agent-id", "", "Agent ID (required)")
	apiKey := flag.String("api-key", "", "API Key for authentication (required)")
	interval := flag.Int("interval", 60, "Reporting interval in seconds")
	diskPath := flag.String("disk-path", "/", "Disk path to monitor")
	allowRemoteDisable := flag.Bool("allow-remote-disable", false, "Allow the backend to disable and uninstall this service when the monitor is deleted")
	remoteDisableCommand := flag.String("remote-disable-command", "", "Command or script path to run when remote disable is allowed")
	flag.Parse()

	// Validate required flags
	if *backendURL == "" || *agentID == "" || *apiKey == "" {
		log.Fatal("backend-url, agent-id, and api-key are required")
	}

	log.Printf("Starting probara agent...")
	log.Printf("Agent ID: %s", *agentID)
	log.Printf("Backend URL: %s", *backendURL)
	log.Printf("Reporting interval: %d seconds", *interval)
	log.Printf("Remote disable: %t", *allowRemoteDisable)

	// Create collector and reporter
	col := collector.NewCollector(*diskPath)
	rep := reporter.NewReporter(*backendURL, *agentID, *apiKey)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start metrics collection loop
	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	// Collect and report immediately on startup
	if err := collectAndReport(ctx, col, rep); err != nil {
		if handleReportError(err, *allowRemoteDisable, *remoteDisableCommand) {
			return
		}
		log.Printf("Warning: initial metrics collection failed: %v", err)
	}

	log.Println("Agent started successfully. Press Ctrl+C to stop.")

	// Main loop
	for {
		select {
		case <-ticker.C:
			if err := collectAndReport(ctx, col, rep); err != nil {
				if handleReportError(err, *allowRemoteDisable, *remoteDisableCommand) {
					return
				}
				log.Printf("Error: %v", err)
			}
		case sig := <-sigChan:
			log.Printf("Received signal %v, shutting down gracefully...", sig)
			return
		}
	}
}

func collectAndReport(ctx context.Context, col *collector.Collector, rep *reporter.Reporter) error {
	// Collect metrics
	metrics, err := col.Collect(ctx)
	if err != nil {
		return err
	}

	log.Printf("Collected metrics: CPU=%.2f%%, Memory=%dMB/%dMB, Disk=%dGB/%dGB, Load=%.2f,%.2f,%.2f",
		metrics.CPUPercent,
		metrics.MemoryUsed/(1024*1024),
		metrics.MemoryTotal/(1024*1024),
		metrics.DiskUsed/(1024*1024*1024),
		metrics.DiskTotal/(1024*1024*1024),
		metrics.LoadAvg1,
		metrics.LoadAvg5,
		metrics.LoadAvg15,
	)

	// Report to backend
	if err := rep.Report(ctx, metrics); err != nil {
		return err
	}

	log.Println("Metrics reported successfully")
	return nil
}

func handleReportError(err error, allowRemoteDisable bool, remoteDisableCommand string) bool {
	if !errors.Is(err, reporter.ErrRemoteDisabled) {
		return false
	}

	if !allowRemoteDisable {
		log.Printf("Server says this agent monitor is deleted, but remote disable is not enabled.")
		return false
	}

	if remoteDisableCommand == "" {
		log.Printf("Server says this agent monitor is deleted, but no remote disable command is configured.")
		return false
	}

	log.Printf("Server says this agent monitor is deleted. Running remote disable command and exiting.")
	if err := startRemoteDisable(remoteDisableCommand); err != nil {
		log.Printf("Failed to start remote disable command: %v", err)
		return false
	}
	return true
}

func startRemoteDisable(command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/C", "start", "", "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", command)
		return cmd.Start()
	} else {
		cmd = exec.Command("sh", command)
	}
	return cmd.Run()
}
