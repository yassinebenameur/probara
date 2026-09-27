package push

import (
	"context"
	"errors"
	"testing"
)

func TestProcessPushRejectsUnknownStatusBeforeDatabaseAccess(t *testing.T) {
	err := NewService(nil, nil).ProcessPush(context.Background(), "test-token", PushPayload{Status: "dowm"})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected invalid status, got %v", err)
	}
}
