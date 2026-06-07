package statusupdates

import (
	"testing"
)

// unreachableNATSURL points at a port where no NATS server listens.
const unreachableNATSURL = "nats://127.0.0.1:59999"

// Publisher and Subscriber must survive NATS outages: succeed when NATS is
// temporarily unreachable and never stop reconnecting. The nats.go default
// gives up after ~2 minutes and leaves the connection permanently CLOSED,
// silently killing status page live updates.
func TestNewPublisherSurvivesNATSOutage(t *testing.T) {
	pub, err := NewPublisherWithSubject(unreachableNATSURL, "test.subject")
	if err != nil {
		t.Fatalf("NewPublisherWithSubject should retry in the background when NATS is down, got error: %v", err)
	}
	defer pub.Close()

	if pub.nc.Opts.MaxReconnect != -1 {
		t.Errorf("MaxReconnect = %d, want -1 (reconnect forever)", pub.nc.Opts.MaxReconnect)
	}
	if !pub.nc.Opts.RetryOnFailedConnect {
		t.Error("RetryOnFailedConnect = false, want true")
	}
}

func TestNewSubscriberSurvivesNATSOutage(t *testing.T) {
	sub, err := NewSubscriberWithSubject(unreachableNATSURL, "test.subject")
	if err != nil {
		t.Fatalf("NewSubscriberWithSubject should retry in the background when NATS is down, got error: %v", err)
	}
	defer sub.Close()

	if sub.nc.Opts.MaxReconnect != -1 {
		t.Errorf("MaxReconnect = %d, want -1 (reconnect forever)", sub.nc.Opts.MaxReconnect)
	}
	if !sub.nc.Opts.RetryOnFailedConnect {
		t.Error("RetryOnFailedConnect = false, want true")
	}
}
