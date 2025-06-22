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
	getRoomNameStmt         *sqlite.Statement
	createRoomStmt          *sqlite.Statement
	listRoomPlayersStmt     *sqlite.Statement
	checkPlayerStmt         *sqlite.Statement
	addPlayerStmt           *sqlite.Statement
	setRandoSeedStmt        *sqlite.Statement
	deleteAllPlacementsStmt *sqlite.Statement
	addPlacementStmt        *sqlite.Statement
	listRoomPlayerIDsStmt   *sqlite.Statement
	getAllPlacementsStmt    *sqlite.Statement
	addResultPlacementStmt  *sqlite.Statement
}

func openDB(filename string) (*database, error) {
	const schema = `
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS mw_rooms (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	);
	
	CREATE TABLE IF NOT EXISTS mw_players (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id),
		player_id INTEGER PRIMARY KEY,
		nickname TEXT NOT NULL,
		rando_seed INTEGER,

		UNIQUE (rando_id, nickname),
		UNIQUE (rando_id, player_id)
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
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id),
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
	db.getRoomNameStmt = conn.Prepare("SELECT name FROM mw_rooms WHERE id = ?")
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
	db.listRoomPlayersStmt = conn.Prepare("SELECT player_id, nickname, rando_seed FROM mw_players WHERE rando_id = ? ORDER BY nickname")
	db.listRoomPlayerIDsStmt = conn.Prepare("SELECT player_id, rando_seed FROM mw_players WHERE rando_id = ? ORDER BY player_id")
	db.getAllPlacementsStmt = conn.Prepare(`
	SELECT mwpp.player_id, mwpp.group_name, mwpp.item_name, mwpp.location_name
	FROM mw_player_placements mwpp
		JOIN mw_players mwp ON mwpp.player_id = mwp.player_id
	WHERE mwp.rando_id = ?
	ORDER BY mwpp.player_id, mwpp.index_`)
	db.addResultPlacementStmt = conn.Prepare(`
	INSERT INTO mw_result_placements (rando_id, item_player_id, item_name, location_player_id, location_name)
	VALUES (?, ?, ?, ?, ?)`)
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

	return sqlitex.Transaction(db.conn, func() error {
		stmt := db.setRandoSeedStmt
		stmt.BindInt(1, int(rando.Seed))
		stmt.BindInt64(2, playerID)
		if err := sqlitex.Exec(stmt); err != nil {
			return err
		}

		stmt = db.deleteAllPlacementsStmt
		stmt.BindInt64(1, playerID)
		if err := sqlitex.Exec(stmt); err != nil {
			return err
		}

		stmt = db.addPlacementStmt
		for groupName, placements := range rando.Items {
			for i, p := range placements {
				stmt.BindInt64(1, playerID)
				stmt.BindString(2, groupName)
				stmt.BindInt(3, i)
				stmt.BindString(4, p.Item)
				stmt.BindString(5, p.Location)
				if err := sqlitex.Exec(stmt); err != nil {
					return err
				}
			}
		}

		return nil
	})
}

type room struct {
	ID      int64
	Name    string
	Players []player
}

type player struct {
	ID       int64
	Nickname string
	HasSeed  bool
}

func (db *database) getRoomInfo(randoID int64) (room room, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	room.ID = randoID

	stmt := db.getRoomNameStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepOnce(stmt, func() {
		room.Name = stmt.ReadString(0)
	})
	if err == sqlitex.ErrZeroRows {
		err = errRoomNotExist
		return
	}

	stmt = db.listRoomPlayersStmt
	stmt.BindInt64(1, randoID)

	err = sqlitex.StepAll(stmt, func() {
		room.Players = append(room.Players, player{
			ID:       stmt.ReadInt64(0),
			Nickname: stmt.ReadString(1),
			HasSeed:  !stmt.IsNull(2),
		})
	})
	return
}

func (db *database) getAttachedRandos(randoID int64) (worlds []world, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	type player struct {
		playerID int64
		seed     int
	}

	var players []player

	stmt := db.listRoomPlayerIDsStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		players = append(players, player{
			playerID: stmt.ReadInt64(0),
			seed:     stmt.ReadInt32(1),
		})
	})
	if err != nil {
		return
	}
	if len(players) == 0 {
		err = errRoomNotExist
		return
	}

	worlds = make([]world, len(players))
	worldMap := make(map[int64]*world, len(players))
	for i, p := range players {
		worlds[i].playerID = p.playerID
		worlds[i].seed = int64(p.seed)
		worlds[i].placements = map[string][]sphere{}
		worldMap[p.playerID] = &worlds[i]
	}

	stmt = db.getAllPlacementsStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		playerID := stmt.ReadInt64(0)
		groupName := stmt.ReadString(1)
		item := stmt.ReadString(2)
		location := stmt.ReadString(3)

		placements := worldMap[playerID].placements
		placements[groupName] = append(placements[groupName],
			sphere{placement{Item: item, Location: location}})
	})
	return
}

func (db *database) saveShuffleResult(randoID int64, placements []mixedPlacement) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return sqlitex.Transaction(db.conn, func() error {
		stmt := db.addResultPlacementStmt
		for _, p := range placements {
			stmt.BindInt64(1, randoID)
			stmt.BindInt64(2, p.Item.World)
			stmt.BindString(3, p.Item.Name)
			stmt.BindInt64(4, p.Location.World)
			stmt.BindString(5, p.Location.Name)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		return nil
	})
}
