package statuspage

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// Before migration 000114 runs there is no visibility column, so no page can
// be gated: the gate reads every page as public instead of failing them all.
func TestPageAccess_BeforeMigrationEveryPageIsPublic(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	pageID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(pageAccessQuery)).
		WillReturnError(&pq.Error{Code: "42703"}) // undefined_column
	mock.ExpectQuery(regexp.QuoteMeta(legacyPageAccessQuery)).
		WithArgs("acme").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "logo_url", "primary_color"}).AddRow(pageID, "Acme", "", ""))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	acc, err := svc.PageAccess(context.Background(), "acme", "", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != pageID || acc.Visibility != pageaccess.Public {
		t.Fatalf("access = %+v, want a public page", acc)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
