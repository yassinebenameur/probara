package collector

import "testing"

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
