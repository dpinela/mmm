package indexfile

import (
	"errors"
	"log"

	"github.com/dpinela/mmm/internal/sqlite"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type File struct {
	db *sqlite.DB
}

func Open(filename string) (*File, error) {
	const schema = `
	PRAGMA journal_mode = WAL;

	CREATE TABLE IF NOT EXISTS mw_rooms (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	) STRICT;

	CREATE TABLE IF NOT EXISTS is_rooms (
		id INTEGER PRIMARY KEY,
		name TEXT UNIQUE,
		hash INTEGER,
		settings TEXT
	) STRICT;
	`
	// Name is nullable for IS rooms so that once setup is done, a new room can reuse the
	// same name, as people often do.

	db, err := sqlitex.OpenWithSchema(filename, schema)
	if err != nil {
		return nil, err
	}
	return &File{db}, nil
}

func (f *File) Close() {
	f.db.Close()
}

var ErrTooManyRooms = errors.New("too many rooms")

func (f *File) CreateMWRoom() (randoID MWRandoID, name string, err error) {
	const (
		maxRetries = 100
		entropy    = 10
	)

	err = sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp("INSERT INTO mw_rooms (name) VALUES (?) RETURNING id")
		defer stmt.Close()

		for range maxRetries {
			name = generateRoomName(entropy)
			stmt.BindString(1, name)
			err := sqlitex.StepOnce(stmt, func() {
				randoID = MWRandoID(stmt.ReadInt64(0))
			})
			if err == sqlite.ErrConstraintUnique {
				continue
			}
			if err == nil {
				return nil
			}
			return err
		}
		return ErrTooManyRooms
	})
	return
}

func (f *File) FindISRoom(name string) (randoID ISRandoID, err error) {
	err = sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT id FROM is_rooms WHERE name = ?")
		defer stmt.Close()
		stmt.BindString(1, name)
		err := sqlitex.StepOnce(stmt, func() {
			randoID = ISRandoID(stmt.ReadInt64(0))
		})
		if err == nil {
			log.Println("found:", name, "as", randoID)
		}
		if err != sqlitex.ErrZeroRows {
			return err
		}

		stmt = f.db.PrepareTemp("INSERT INTO is_rooms (name) VALUES (?) RETURNING id")
		defer stmt.Close()
		stmt.BindString(1, name)
		return sqlitex.StepOnce(stmt, func() {
			randoID = ISRandoID(stmt.ReadInt64(0))
		})
	})
	return
}

func (f *File) FindRoom(roomName string) (randoID MWRandoID, err error) {
	stmt := f.db.PrepareTemp("SELECT id FROM mw_rooms WHERE name = ?")
	defer stmt.Close()
	stmt.BindString(1, roomName)
	err = sqlitex.StepOnce(stmt, func() {
		randoID = MWRandoID(stmt.ReadInt64(0))
	})
	if err == sqlitex.ErrZeroRows {
		err = ErrRoomNotExist
	}
	return
}

func (f *File) GetName(roomID MWRandoID) (name string, err error) {
	stmt := f.db.PrepareTemp("SELECT name FROM mw_rooms WHERE id = ?")
	defer stmt.Close()
	stmt.BindInt64(1, int64(roomID))
	err = sqlitex.StepOnce(stmt, func() {
		name = stmt.ReadString(0)
	})
	if err == sqlitex.ErrZeroRows {
		err = ErrRoomNotExist
	}
	return
}

func (f *File) DeleteRoom(id MWRandoID) error {
	stmt := f.db.PrepareTemp("DELETE FROM mw_rooms WHERE id = ?")
	defer stmt.Close()
	stmt.BindInt64(1, int64(id))
	return stmt.Exec()
}

var (
	ErrRoomNotExist    = errors.New("room does not exist")
	ErrRoomNotShuffled = errors.New("room is not yet shuffled")
	ErrRoomNotOpen     = errors.New("room is already being shuffled")
)

type RoomStatus int

const (
	RoomStatusOpen RoomStatus = iota
	RoomStatusShuffling
	RoomStatusShuffled
)

type MWRandoID int64
type ISRandoID int64
