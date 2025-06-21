package main

import (
	"errors"
	"sync"

	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlite"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type database struct {
	mu                      sync.Mutex
	conn                    *sqlite.DB
	beginStmt               *sqlite.Statement
	commitStmt              *sqlite.Statement
	rollbackStmt            *sqlite.Statement
	hasMWResultStmt         *sqlite.Statement
	getRoomIDStmt           *sqlite.Statement
	createRoomStmt          *sqlite.Statement
	checkPlayerStmt         *sqlite.Statement
	addPlayerStmt           *sqlite.Statement
	setRandoSeedStmt        *sqlite.Statement
	deleteAllPlacementsStmt *sqlite.Statement
	addPlacementStmt        *sqlite.Statement
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
		rando_seed INTEGER,

		UNIQUE (rando_id, nickname)
	);
	
	CREATE TABLE IF NOT EXISTS mw_player_placements (
		player_id INTEGER NOT NULL REFERENCES mw_players (player_id),
		group_name TEXT NOT NULL,
		index_ INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		location_name TEXT NOT NULL,

		PRIMARY KEY (player_id, group_name, index_)
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
	db.beginStmt = conn.Prepare("BEGIN")
	db.commitStmt = conn.Prepare("COMMIT")
	db.rollbackStmt = conn.Prepare("ROLLBACK")
	db.getRoomIDStmt = conn.Prepare("SELECT id FROM mw_rooms WHERE name = ?")
	db.hasMWResultStmt = conn.Prepare(`
	SELECT EXISTS(SELECT 1
		FROM mw_result_placements mrp
		JOIN mw_rooms mr ON mrp.rando_id = mr.id WHERE name = ?)`)
	db.createRoomStmt = conn.Prepare("INSERT INTO mw_rooms (name) VALUES (?) RETURNING id")
	db.checkPlayerStmt = conn.Prepare("SELECT player_id FROM mw_players WHERE rando_id = ? AND nickname = ?")
	db.addPlayerStmt = conn.Prepare("INSERT INTO mw_players (rando_id, nickname) VALUES (?, ?) RETURNING player_id")
	db.setRandoSeedStmt = conn.Prepare("UPDATE mw_players SET rando_seed = ? WHERE player_id = ?")
	db.deleteAllPlacementsStmt = conn.Prepare("DELETE FROM mw_player_placements WHERE player_id = ?")
	db.addPlacementStmt = conn.Prepare("INSERT INTO mw_player_placements (player_id, group_name, index_, item_name, location_name) VALUES (?, ?, ?, ?, ?)")
	return db, nil
}

var (
	errRoomNotExist = errors.New("room does not exist")
)

func (db *database) joinRoom(roomName string, nickname string) (rid int64, pid int64, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getRoomIDStmt
	stmt.BindString(1, roomName)
	err = sqlitex.StepOnce(stmt, func() {
		rid = stmt.ReadInt64(0)
	})
	if err == sqlitex.ErrZeroRows {
		err = errRoomNotExist
		return
	}

	stmt = db.checkPlayerStmt
	stmt.BindInt64(1, rid)
	stmt.BindString(2, nickname)
	err = sqlitex.StepOnce(stmt, func() {
		pid = stmt.ReadInt64(0)
	})
	if err != sqlitex.ErrZeroRows {
		return
	}
	stmt = db.addPlayerStmt
	stmt.BindInt64(1, rid)
	stmt.BindString(2, nickname)
	err = sqlitex.StepOnce(stmt, func() {
		pid = stmt.ReadInt64(0)
	})
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

func (db *database) attachRando(playerID int64, rando mwproto.RandoGeneratedMessage) (err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err = sqlitex.Exec(db.beginStmt); err != nil {
		return
	}

	defer func() {
		if err != nil {
			sqlitex.Exec(db.rollbackStmt)
		}
	}()

	stmt := db.setRandoSeedStmt
	stmt.BindInt(1, int(rando.Seed))
	stmt.BindInt64(2, playerID)
	if err = sqlitex.Exec(stmt); err != nil {
		return
	}

	stmt = db.deleteAllPlacementsStmt
	stmt.BindInt64(1, playerID)
	if err = sqlitex.Exec(stmt); err != nil {
		return
	}

	stmt = db.addPlacementStmt
	for groupName, placements := range rando.Items {
		for i, p := range placements {
			stmt.BindInt64(1, playerID)
			stmt.BindString(2, groupName)
			stmt.BindInt(3, i)
			stmt.BindString(4, p.Item)
			stmt.BindString(5, p.Location)
			if err = sqlitex.Exec(stmt); err != nil {
				return
			}
		}
	}

	return sqlitex.Exec(db.commitStmt)
}
