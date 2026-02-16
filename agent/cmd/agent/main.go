package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
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
	flag.Parse()

	// Validate required flags
	if *backendURL == "" || *agentID == "" || *apiKey == "" {
		log.Fatal("backend-url, agent-id, and api-key are required")
	}

	log.Printf("Starting probara agent...")
	log.Printf("Agent ID: %s", *agentID)
	log.Printf("Backend URL: %s", *backendURL)
	log.Printf("Reporting interval: %d seconds", *interval)

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
		log.Printf("Warning: initial metrics collection failed: %v", err)
	}

	log.Println("Agent started successfully. Press Ctrl+C to stop.")

	// Main loop
	for {
		select {
		case <-ticker.C:
			if err := collectAndReport(ctx, col, rep); err != nil {
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
