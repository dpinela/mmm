package main

import (
	"sync"

	"github.com/dpinela/mmm/internal/sqlite"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type database struct {
	mu              sync.Mutex
	conn            *sqlite.DB
	hasMWResultStmt *sqlite.Statement
	getRoomIDStmt   *sqlite.Statement
	createRoomStmt  *sqlite.Statement
}

func openDB(filename string) (*database, error) {
	const schema = `
	CREATE TABLE IF NOT EXISTS mw_rooms (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	);
	
	CREATE TABLE IF NOT EXISTS mw_players (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (rando_id),
		player_id INTEGER PRIMARY KEY,
		nickname TEXT NOT NULL,

		UNIQUE (rando_id, nickname)
	);
	
	CREATE TABLE IF NOT EXISTS mw_player_placements (
		player_id INTEGER NOT NULL REFERENCES mw_players (player_id),
		index_ INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		location_name TEXT NOT NULL,

		PRIMARY KEY (player_id, index_)
	);
	
	CREATE TABLE IF NOT EXISTS mw_ready_metadata (
		player_id INTEGER NOT NULL REFERENCES mw_players (player_id),
		index_ INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,

		PRIMARY KEY (player_id, index_)
	);
	
	CREATE TABLE IF NOT EXISTS mw_result_placements (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (rando_id),
		item_player_id INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		location_player_id INTEGER NOT NULL,
		location_name TEXT NOT NULL,

		FOREIGN KEY (rando_id, item_player_id) REFERENCES mw_players (rando_id, player_id),
		FOREIGN KEY (rando_id, location_player_id) REFERENCES mw_players (rando_id, player_id)
	);`

	conn, err := sqlite.Open(filename)
	if err != nil {
		return nil, err
	}
	err = conn.Exec(schema)
	if err != nil {
		conn.Close()
		return nil, err
	}
	db := &database{conn: conn}
	db.getRoomIDStmt = conn.Prepare("SELECT id FROM mw_rooms WHERE name = ?")
	db.hasMWResultStmt = conn.Prepare(`
	SELECT EXISTS(SELECT 1
		FROM mw_result_placements mrp
		JOIN mw_rooms mr ON mrp.rando_id = mr.id WHERE name = ?)`)
	db.createRoomStmt = conn.Prepare("INSERT INTO mw_rooms (name) VALUES (?) RETURNING id")
	return db, nil
}

func (db *database) idOfRoom(roomName string) (id int64, found bool, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getRoomIDStmt
	stmt.BindString(1, roomName)
	err = sqlitex.StepOnce(stmt, func() {
		id = stmt.ReadInt64(0)
		found = true
	})
	if err == sqlitex.ErrZeroRows {
		err = nil
	}
	return
}

func (db *database) createRoom(roomName string) (id int64, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.createRoomStmt
	stmt.BindString(1, roomName)
	err = sqlitex.StepOnce(stmt, func() {
		id = stmt.ReadInt64(0)
	})
	return
}
