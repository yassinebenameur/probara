package config

import (
	"fmt"
	"os"
	"strings"
)

// Ping socket modes (WORKER_PING_MODE).
const (
	// PingModeUnprivileged sends echoes over Linux "ping" datagram sockets.
	// It needs no capability, only the process's group inside the kernel's
	// net.ipv4.ping_group_range, which the Helm chart and Compose set.
	PingModeUnprivileged = "unprivileged"
	// PingModePrivileged opens raw ICMP sockets: CAP_NET_RAW, which in
	// Kubernetes means running as root with the capability added.
	PingModePrivileged = "privileged"
	// PingModeAuto tries unprivileged and falls back to raw sockets only
	// when the datagram socket is refused for lack of permission.
	PingModeAuto = "auto"
)

// loadWorkerPingMode reads WORKER_PING_MODE (default unprivileged). An
// unknown value fails startup rather than silently picking a mode.
func loadWorkerPingMode() (string, error) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WORKER_PING_MODE")))
	switch v {
	case "":
		return PingModeUnprivileged, nil
	case PingModeUnprivileged, PingModePrivileged, PingModeAuto:
		return v, nil
	default:
		return "", fmt.Errorf("invalid WORKER_PING_MODE %q: must be unprivileged, privileged or auto", v)
	}
}
