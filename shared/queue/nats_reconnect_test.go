package queue

import (
	"testing"
)

// unreachableNATSURL points at a port where no NATS server listens.
const unreachableNATSURL = "nats://127.0.0.1:59999"

// NewClient must not fail when NATS is temporarily unreachable: the client
// should be created and keep retrying in the background. Without this, a
// NATS outage at startup (or a pod reschedule) crashes the service instead
// of letting it recover on its own.
func TestNewClientSucceedsWhenNATSUnreachable(t *testing.T) {
	client, err := NewClient(unreachableNATSURL)
	if err != nil {
		t.Fatalf("NewClient should retry in the background when NATS is down, got error: %v", err)
	}
	defer client.Close()

	if client.nc.IsClosed() {
		t.Fatal("connection should be waiting to reconnect, not permanently closed")
	}
}

// Closed must distinguish "reconnecting" (alive, leave the pod alone) from
// "permanently closed" (zombie, liveness probe should fail so the pod is
// restarted).
func TestClientClosed(t *testing.T) {
	client, err := NewClient(unreachableNATSURL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if client.Closed() {
		t.Fatal("client should not report closed while reconnecting")
	}

	client.Close()

	if !client.Closed() {
		t.Fatal("client should report closed after Close()")
	}
}

// The connection must never give up reconnecting. The nats.go default
// (MaxReconnect=60, ~2 minutes) leaves the connection permanently CLOSED
// after a longer NATS outage, freezing every consumer until pod restart.
func TestNewClientNeverStopsReconnecting(t *testing.T) {
	client, err := NewClient(unreachableNATSURL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	opts := client.nc.Opts
	if opts.MaxReconnect != -1 {
		t.Errorf("MaxReconnect = %d, want -1 (reconnect forever)", opts.MaxReconnect)
	}
	if !opts.RetryOnFailedConnect {
		t.Error("RetryOnFailedConnect = false, want true")
	}
	if opts.ReconnectWait <= 0 {
		t.Errorf("ReconnectWait = %v, want > 0", opts.ReconnectWait)
	}
}
