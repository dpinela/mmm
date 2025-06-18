package sqlitex

import (
	"errors"

	"github.com/dpinela/mmm/internal/sqlite"
)

var (
	ErrZeroRows     = errors.New("statement returned no rows")
	ErrMultipleRows = errors.New("statement returned multiple rows")
)

func StepOnce(stmt *sqlite.Statement, rowHandler func()) error {
	defer stmt.Reset()
	hasRow, err := stmt.Step()
	if err != nil {
		return err
	}
	if !hasRow {
		return ErrZeroRows
	}
	rowHandler()
	hasRow, err = stmt.Step()
	if err != nil {
		return err
	}
	if hasRow {
		return ErrMultipleRows
	}
	return stmt.Reset()
}
