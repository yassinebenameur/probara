package statusupdates

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	defaultSubject = "statuspage.updates"
	subjectEnvVar  = "STATUSPAGE_UPDATES_SUBJECT"
)

// Event represents a status page update signal.
type Event struct {
	Type         string    `json:"type"`
	MonitorID    string    `json:"monitor_id,omitempty"`
	StatusPageID string    `json:"status_page_id,omitempty"`
	TenantID     string    `json:"tenant_id,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// reconnectOptions keeps the connection retrying forever. The nats.go
// default gives up after 60 attempts (~2 minutes) and leaves the connection
// permanently CLOSED, silently breaking status page live updates after a
// NATS outage. Keep in sync with shared/queue.
func reconnectOptions() []nats.Option {
	return []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("nats: disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("nats: reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			log.Printf("nats: connection permanently closed")
		}),
	}
}

// SubjectFromEnv returns the configured subject or the default.
func SubjectFromEnv() string {
	if value := os.Getenv(subjectEnvVar); value != "" {
		return value
	}
	return defaultSubject
}

// Publisher publishes status update events to NATS (core).
type Publisher struct {
	nc      *nats.Conn
	subject string
}

// NewPublisher creates a new publisher using the env-configured subject.
func NewPublisher(natsURL string) (*Publisher, error) {
	return NewPublisherWithSubject(natsURL, SubjectFromEnv())
}

// NewPublisherWithSubject creates a new publisher with the given subject.
func NewPublisherWithSubject(natsURL, subject string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL, reconnectOptions()...)
	if err != nil {
		return nil, err
	}
	return &Publisher{nc: nc, subject: subject}, nil
}

// Publish sends an event to the configured subject.
func (p *Publisher) Publish(event Event) error {
	if p == nil || p.nc == nil {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.nc.Publish(p.subject, data)
}

// Close closes the underlying NATS connection.
func (p *Publisher) Close() {
	if p == nil || p.nc == nil {
		return
	}
	p.nc.Close()
}

// Subscriber receives status update events from NATS (core).
type Subscriber struct {
	nc      *nats.Conn
	subject string
}

// NewSubscriber creates a new subscriber using the env-configured subject.
func NewSubscriber(natsURL string) (*Subscriber, error) {
	return NewSubscriberWithSubject(natsURL, SubjectFromEnv())
}

// NewSubscriberWithSubject creates a new subscriber with the given subject.
func NewSubscriberWithSubject(natsURL, subject string) (*Subscriber, error) {
	nc, err := nats.Connect(natsURL, reconnectOptions()...)
	if err != nil {
		return nil, err
	}
	return &Subscriber{nc: nc, subject: subject}, nil
}

// Subscribe registers a handler for status update events.
func (s *Subscriber) Subscribe(handler func(Event)) (*nats.Subscription, error) {
	if s == nil || s.nc == nil {
		return nil, nil
	}
	return s.nc.Subscribe(s.subject, func(msg *nats.Msg) {
		var event Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			return
		}
		handler(event)
	})
}

// Close closes the underlying NATS connection.
func (s *Subscriber) Close() {
	if s == nil || s.nc == nil {
		return
	}
	s.nc.Close()
}
