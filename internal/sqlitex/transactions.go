package sqlitex

import (
	"errors"
	"time"

	"github.com/dpinela/mmm/internal/sqlite"
)

func Transaction(db *sqlite.DB, tx func() error) (err error) {
	return RetryWhileBusy(func() error {
		return transaction(db, "BEGIN", tx)
	})
}

func WriteTransaction(db *sqlite.DB, tx func() error) (err error) {
	return RetryWhileBusy(func() error {
		return transaction(db, "BEGIN IMMEDIATE", tx)
	})
}

func RetryWhileBusy(op func() error) (err error) {
	const (
		timeout             = 5 * time.Second
		timeBetweenAttempts = 10 * time.Millisecond
	)

	start := time.Now()
	for {
		err = op()
		if !errors.Is(err, sqlite.ErrBusy) || time.Since(start) > timeout {
			return
		}
		time.Sleep(timeBetweenAttempts)
	}
}

func transaction(db *sqlite.DB, initStmt string, tx func() error) (err error) {
	if err = db.Exec(initStmt); err != nil {
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
