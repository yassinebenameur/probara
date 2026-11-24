package statuspage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// One migration behind (no 000122 incident column), the claim reads every
// delivery as a monitor transition instead of failing.
func TestPushStore_ClaimBeforeIncidentColumn(t *testing.T) {
	errVariant := errors.New("query variant mismatch")
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		// expected names the variant that must come: "with" or "without".
		if (expected == "with") != strings.Contains(actual, pushDeliveryIncidentColumn) {
			return errVariant
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := newPushStore(sqlDB)

	mock.ExpectQuery("with").WillReturnError(&pq.Error{Code: "42703"})
	mock.ExpectQuery("without").WillReturnRows(sqlmock.NewRows([]string{"id", "page", "slug", "title", "monitor", "name", "kind", "deliverable", "incident"}).
		AddRow(uuid.New(), uuid.New(), "acme", "Acme", uuid.New(), "API", "down", true, nil))
	claimed, err := store.ClaimDeliveries(context.Background(), 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].IncidentID != uuid.Nil {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
