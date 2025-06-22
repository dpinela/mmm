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

func StepAll(stmt *sqlite.Statement, rowHandler func()) error {
	defer stmt.Reset()
	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return err
		}
		if !hasRow {
			return stmt.Reset()
		}
		rowHandler()
	}
}

func Exec(stmt *sqlite.Statement) error {
	defer stmt.Reset()
	return stmt.Exec()
}

func Transaction(db *sqlite.DB, tx func() error) (err error) {
	if err = db.Exec("BEGIN"); err != nil {
		return
	}
	defer func() {
		if err != nil {
			db.Exec("ROLLBACK")
		}
	}()
	if err = tx(); err != nil {
		return
	}
	return db.Exec("COMMIT")
}
