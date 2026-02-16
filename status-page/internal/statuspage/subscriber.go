package statuspage

import (
	"encoding/json"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// Subscriber listens for status update events and broadcasts to SSE clients.
type Subscriber struct {
	subject *statusupdates.Subscriber
	hub     *Hub
	logger  *logger.Logger
}

// NewSubscriber creates a new status update subscriber.
func NewSubscriber(natsURL string, hub *Hub, log *logger.Logger) (*Subscriber, error) {
	sub, err := statusupdates.NewSubscriber(natsURL)
	if err != nil {
		return nil, err
	}
	return &Subscriber{
		subject: sub,
		hub:     hub,
		logger:  log,
	}, nil
}

// Start subscribes to NATS and begins broadcasting.
func (s *Subscriber) Start() error {
	if s.subject == nil || s.hub == nil {
		return nil
	}
	_, err := s.subject.Subscribe(func(event statusupdates.Event) {
		payload, err := json.Marshal(event)
		if err != nil {
			return
		}
		s.hub.BroadcastAll(SSEEvent{
			Type: "update",
			Data: string(payload),
		})
	})
	if err != nil {
		return err
	}
	s.logger.WithFields(map[string]interface{}{
		"subject": statusupdates.SubjectFromEnv(),
	}).Info("Status page update subscriber started")
	return nil
}

// Close closes the subscription connection.
func (s *Subscriber) Close() {
	if s.subject != nil {
		s.subject.Close()
	}
}
