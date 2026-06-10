package scheduler

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/lib/pq"
)

func TestIsTransientRollupError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "context canceled", err: context.Canceled, want: true},
		{name: "driver bad conn", err: driver.ErrBadConn, want: true},
		{name: "io EOF", err: io.EOF, want: true},
		{name: "io unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "wrapped io EOF", err: fmt.Errorf("failed to upsert daily rollup: %w", io.EOF), want: true},
		{name: "pq deadlock detected (40P01)", err: &pq.Error{Code: "40P01"}, want: true},
		{name: "pq too many connections (53300)", err: &pq.Error{Code: "53300"}, want: true},
		{name: "pq query canceled (57014)", err: &pq.Error{Code: "57014"}, want: true},
		{name: "pq system error (58000)", err: &pq.Error{Code: "58000"}, want: true},
		{name: "wrapped pq serialization class (40001)", err: fmt.Errorf("failed to commit rollup transaction: %w", &pq.Error{Code: "40001"}), want: true},
		{name: "pq unique violation (23505)", err: &pq.Error{Code: "23505"}, want: false},
		{name: "generic error", err: errors.New("boom"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientRollupError(tc.err); got != tc.want {
				t.Errorf("isTransientRollupError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
