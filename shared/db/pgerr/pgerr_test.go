package pgerr

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func TestPredicates(t *testing.T) {
	unique := &pq.Error{Code: "23505", Constraint: "users_email_key"}
	table := &pq.Error{Code: "42P01"}
	column := &pq.Error{Code: "42703"}

	for _, tc := range []struct {
		name                                 string
		err                                  error
		unique, table, column, missingSchema bool
	}{
		{name: "nil"},
		{name: "plain error", err: errors.New("23505")},
		{name: "no rows", err: sql.ErrNoRows},
		{name: "unique violation", err: unique, unique: true},
		{name: "wrapped unique violation", err: fmt.Errorf("insert user: %w", unique), unique: true},
		{name: "undefined table", err: table, table: true, missingSchema: true},
		{name: "wrapped undefined table", err: fmt.Errorf("query: %w", table), table: true, missingSchema: true},
		{name: "undefined column", err: column, column: true, missingSchema: true},
		{name: "other code", err: &pq.Error{Code: "23503"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUniqueViolation(tc.err); got != tc.unique {
				t.Errorf("IsUniqueViolation = %v, want %v", got, tc.unique)
			}
			if got := IsUndefinedTable(tc.err); got != tc.table {
				t.Errorf("IsUndefinedTable = %v, want %v", got, tc.table)
			}
			if got := IsUndefinedColumn(tc.err); got != tc.column {
				t.Errorf("IsUndefinedColumn = %v, want %v", got, tc.column)
			}
			if got := IsMissingSchema(tc.err); got != tc.missingSchema {
				t.Errorf("IsMissingSchema = %v, want %v", got, tc.missingSchema)
			}
		})
	}
}

func TestAs(t *testing.T) {
	unique := &pq.Error{Code: "23505", Constraint: "users_email_key"}
	got, ok := As(fmt.Errorf("insert: %w", unique))
	if !ok || got.Constraint != "users_email_key" {
		t.Fatalf("As = %v, %v; want the wrapped pq error", got, ok)
	}
	if !IsUniqueViolationOf(fmt.Errorf("insert: %w", unique), "users_email_key") {
		t.Fatal("IsUniqueViolationOf missed the violated constraint")
	}
	if IsUniqueViolationOf(unique, "users_username_key") {
		t.Fatal("IsUniqueViolationOf matched another constraint")
	}
	if IsUniqueViolationOf(&pq.Error{Code: "23503", Constraint: "users_email_key"}, "users_email_key") {
		t.Fatal("IsUniqueViolationOf matched a code that is not unique_violation")
	}
	if _, ok := As(errors.New("boom")); ok {
		t.Fatal("As matched an error that is not a Postgres error")
	}
}
