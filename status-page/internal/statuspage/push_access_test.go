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

// Until migration 000114 runs there is no visibility column. The push paths
// then retry without the visibility predicate (no page can be gated yet)
// instead of failing every subscribe, reconcile and claim.
func TestPushStore_BeforeMigrationFallsBackWithoutVisibility(t *testing.T) {
	undefinedColumn := &pq.Error{Code: "42703"}
	withVisibility := func(q string) bool { return strings.Contains(q, pushVisiblePages) }

	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		// expected is "with" or "without": which variant of the query must come.
		if (expected == "with") != withVisibility(actual) {
			return errMatch
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := newPushStore(sqlDB)
	ctx := context.Background()
	pageID := uuid.New()

	mock.ExpectQuery("with").WillReturnError(undefinedColumn)
	mock.ExpectQuery("without").WithArgs("acme").WillReturnRows(sqlmock.NewRows([]string{"id", "enabled"}).AddRow(pageID, true))
	id, enabled, err := store.PageIDForSlug(ctx, "acme")
	if err != nil || id != pageID || !enabled {
		t.Fatalf("PageIDForSlug = %v %v %v", id, enabled, err)
	}

	mock.ExpectExec("with").WillReturnError(undefinedColumn)
	mock.ExpectExec("without").WillReturnResult(sqlmock.NewResult(0, 2))
	if n, err := store.reconcileNotifications(ctx); err != nil || n != 2 {
		t.Fatalf("reconcile = %d %v", n, err)
	}

	mock.ExpectQuery("with").WillReturnError(undefinedColumn)
	mock.ExpectQuery("without").WillReturnRows(sqlmock.NewRows([]string{"id", "page", "slug", "title", "monitor", "name", "kind", "deliverable"}).
		AddRow(uuid.New(), pageID, "acme", "Acme", uuid.New(), "API", "down", true))
	claimed, err := store.ClaimDeliveries(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var errMatch = errors.New("query variant mismatch")
