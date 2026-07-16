package queue

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestConsumerStateRequiresReset(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		streamInfo   *jetstream.StreamInfo
		consumerInfo *jetstream.ConsumerInfo
		want         bool
	}{
		{
			name: "matching stream progress does not reset",
			streamInfo: &jetstream.StreamInfo{
				State: jetstream.StreamState{LastSeq: 42},
			},
			consumerInfo: &jetstream.ConsumerInfo{
				Created: now,
				Delivered: jetstream.SequenceInfo{
					Stream: 42,
				},
				AckFloor: jetstream.SequenceInfo{
					Stream: 42,
				},
			},
			want: false,
		},
		{
			name: "consumer delivered beyond current stream resets",
			streamInfo: &jetstream.StreamInfo{
				State: jetstream.StreamState{LastSeq: 42},
			},
			consumerInfo: &jetstream.ConsumerInfo{
				Created: now,
				Delivered: jetstream.SequenceInfo{
					Stream: 11386,
				},
				AckFloor: jetstream.SequenceInfo{
					Stream: 11386,
				},
			},
			want: true,
		},
		{
			name: "empty recreated stream with stale consumer resets",
			streamInfo: &jetstream.StreamInfo{
				State: jetstream.StreamState{LastSeq: 0},
			},
			consumerInfo: &jetstream.ConsumerInfo{
				Created: now,
				Delivered: jetstream.SequenceInfo{
					Stream: 7,
				},
				AckFloor: jetstream.SequenceInfo{
					Stream: 7,
				},
			},
			want: true,
		},
		{
			name: "nil info does not reset",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := consumerStateRequiresReset(tt.streamInfo, tt.consumerInfo)
			if got != tt.want {
				t.Fatalf("consumerStateRequiresReset() = %v, want %v", got, tt.want)
			}
		})
	}
}
