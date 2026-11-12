// Package pgerr names the Postgres error codes the services branch on, so a
// code is spelled once and every check unwraps the error the same way.
package pgerr

import (
	"errors"

	"github.com/lib/pq"
)

const (
	codeUniqueViolation = "23505"
	codeUndefinedTable  = "42P01"
	codeUndefinedColumn = "42703"
)

// As returns the Postgres error anywhere in err's chain, for callers that need
// more than the code (the violated constraint's name, say).
func As(err error) (*pq.Error, bool) {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return nil, false
	}
	return pqErr, true
}

func hasCode(err error, codes ...pq.ErrorCode) bool {
	pqErr, ok := As(err)
	if !ok {
		return false
	}
	for _, code := range codes {
		if pqErr.Code == code {
			return true
		}
	}
	return false
}

// IsUniqueViolation reports unique_violation (23505).
func IsUniqueViolation(err error) bool {
	return hasCode(err, codeUniqueViolation)
}

// IsUniqueViolationOf reports a unique_violation of the named constraint or
// unique index.
func IsUniqueViolationOf(err error, constraint string) bool {
	pqErr, ok := As(err)
	return ok && pqErr.Code == codeUniqueViolation && pqErr.Constraint == constraint
}

// IsUndefinedTable reports undefined_table (42P01).
func IsUndefinedTable(err error) bool {
	return hasCode(err, codeUndefinedTable)
}

// IsUndefinedColumn reports undefined_column (42703).
func IsUndefinedColumn(err error) bool {
	return hasCode(err, codeUndefinedColumn)
}

// IsMissingSchema reports undefined_table or undefined_column: the binary is
// running ahead of the migrations job. Callers use it to fall back to the
// previous schema's query instead of failing during a rollout.
func IsMissingSchema(err error) bool {
	return hasCode(err, codeUndefinedTable, codeUndefinedColumn)
}
