package ingest

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

func mustMarshal(t testing.TB, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// testMessage wraps raw data in a queue.Message with no-op ack callbacks.
func testMessage(data []byte) *queue.Message {
	return &queue.Message{
		Data:       data,
		Subject:    models.CheckResultSubject,
		Ack:        func() error { return nil },
		Nak:        func() error { return nil },
		InProgress: func() error { return nil },
	}
}
