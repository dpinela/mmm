package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
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
	checkRoomJoinableStmt   *sqlite.Statement
	getRoomInfoByNameStmt   *sqlite.Statement
	getRoomInfoByIDStmt     *sqlite.Statement
	createRoomStmt          *sqlite.Statement
	listRoomPlayersStmt     *sqlite.Statement
	checkPlayerStmt         *sqlite.Statement
	addPlayerStmt           *sqlite.Statement
	setRandoSeedStmt        *sqlite.Statement
	deleteAllPlacementsStmt *sqlite.Statement
	addPlacementStmt        *sqlite.Statement
	getPlayerSeedStmt       *sqlite.Statement
	getPlayerPlacementsStmt *sqlite.Statement
	setRoomStatusStmt       *sqlite.Statement
	getAllPlacementsStmt    *sqlite.Statement
	addResultPlacementStmt  *sqlite.Statement
	getResultPlacementsStmt *sqlite.Statement
	sendItemStmt            *sqlite.Statement
	getSentItemsStmt        *sqlite.Statement
	confirmItemStmt         *sqlite.Statement
	markItemsAsSavedStmt    *sqlite.Statement
	hasNotchCostsStmt       *sqlite.Statement
	markNotchCostsGotStmt   *sqlite.Statement
	addNotchCostStmt        *sqlite.Statement
	getNotchCostsStmt       *sqlite.Statement
	confirmNotchCostsStmt   *sqlite.Statement
}

func openDB(filename string) (*database, error) {
	const schema = `
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS mw_rooms (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		status INTEGER NOT NULL
	);
	
	CREATE TABLE IF NOT EXISTS mw_players (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id) ON DELETE CASCADE,
		player_id INTEGER NOT NULL CHECK (player_id >= 0),
		nickname TEXT NOT NULL,
		rando_seed INTEGER,
		has_notch_costs INTEGER NOT NULL,

		UNIQUE (rando_id, nickname),
		PRIMARY KEY (rando_id, player_id)
	);
	
	CREATE TABLE IF NOT EXISTS mw_player_placements (
		rando_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		group_name TEXT NOT NULL,
		index_ INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		location_name TEXT NOT NULL,

		FOREIGN KEY (rando_id, player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,

		PRIMARY KEY (rando_id, player_id, group_name, index_)
	);
	
	CREATE TABLE IF NOT EXISTS mw_ready_metadata (
		rando_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		index_ INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,

		FOREIGN KEY (rando_id, player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,

		PRIMARY KEY (rando_id, player_id, index_)
	);
	
	CREATE TABLE IF NOT EXISTS mw_result_placements (
		rando_id INTEGER NOT NULL REFERENCES mw_rooms (id),
		item_player_id INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		group_name TEXT NOT NULL,
		location_player_id INTEGER NOT NULL,
		location_name TEXT NOT NULL,
		location_index INTEGER NOT NULL,

		FOREIGN KEY (rando_id, item_player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,
		FOREIGN KEY (rando_id, location_player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE
	);
	
	CREATE TABLE IF NOT EXISTS mw_sent_items (
		rando_id INTEGER NOT NULL,
		sender_id INTEGER NOT NULL,
		destination_player_id INTEGER NOT NULL,
		label TEXT NOT NULL,
		content TEXT NOT NULL,
		status INTEGER NOT NULL,

		FOREIGN KEY (rando_id, sender_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,
		FOREIGN KEY (rando_id, destination_player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS unconfirmed_items_by_recipient ON mw_sent_items (rando_id, destination_player_id);

	CREATE TABLE IF NOT EXISTS mw_notch_costs (
		rando_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		charm INTEGER NOT NULL,
		cost INTEGER NOT NULL,

		FOREIGN KEY (rando_id, player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,

		PRIMARY KEY (rando_id, player_id, charm)
	);

	CREATE TABLE IF NOT EXISTS mw_confirmed_notch_costs (
		rando_id INTEGER NOT NULL,
		sender_id INTEGER NOT NULL,
		destination_player_id INTEGER NOT NULL,

		FOREIGN KEY (rando_id, sender_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,
		FOREIGN KEY (rando_id, destination_player_id) REFERENCES mw_players (rando_id, player_id) ON DELETE CASCADE,

		PRIMARY KEY (rando_id, sender_id, destination_player_id)
	);
	`

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
	db.checkRoomJoinableStmt = conn.Prepare(`
	SELECT mr.status FROM mw_players mp JOIN mw_rooms mr ON mp.rando_id = mr.id
	WHERE mp.rando_id = ? AND mp.player_id = ?`)
	db.getRoomInfoByNameStmt = conn.Prepare("SELECT id, status FROM mw_rooms WHERE name = ?")
	db.getRoomInfoByIDStmt = conn.Prepare("SELECT name FROM mw_rooms WHERE id = ?")
	db.createRoomStmt = conn.Prepare("INSERT INTO mw_rooms (name, status) VALUES (?, 0) RETURNING id")
	db.checkPlayerStmt = conn.Prepare("SELECT player_id FROM mw_players WHERE rando_id = ? AND nickname = ?")
	db.addPlayerStmt = conn.Prepare("INSERT INTO mw_players (rando_id, player_id, nickname, has_notch_costs) VALUES (?1, (SELECT COUNT(*) FROM mw_players WHERE rando_id = ?1), ?2, 0) RETURNING player_id")
	db.setRandoSeedStmt = conn.Prepare("UPDATE mw_players SET rando_seed = ? WHERE player_id = ?")
	db.deleteAllPlacementsStmt = conn.Prepare("DELETE FROM mw_player_placements WHERE player_id = ?")
	db.addPlacementStmt = conn.Prepare("INSERT INTO mw_player_placements (rando_id, player_id, group_name, index_, item_name, location_name) VALUES (?, ?, ?, ?, ?, ?)")
	db.getPlayerSeedStmt = conn.Prepare("SELECT rando_seed FROM mw_players WHERE rando_id = ? AND player_id = ?")
	db.getPlayerPlacementsStmt = conn.Prepare("SELECT group_name, item_name, location_name FROM mw_player_placements WHERE rando_id = ? AND player_id = ? ORDER BY index_")
	db.listRoomPlayersStmt = conn.Prepare("SELECT nickname, rando_seed FROM mw_players WHERE rando_id = ? ORDER BY player_id")
	db.setRoomStatusStmt = conn.Prepare("UPDATE mw_rooms SET status = ? WHERE id = ?")
	db.getAllPlacementsStmt = conn.Prepare(`
	SELECT player_id, group_name, item_name, location_name, index_
	FROM mw_player_placements
	WHERE rando_id = ?
	ORDER BY player_id, index_`)
	db.addResultPlacementStmt = conn.Prepare(`
	INSERT INTO mw_result_placements (rando_id, group_name, item_player_id, item_name, location_player_id, location_name, location_index)
	VALUES (?, ?, ?, ?, ?, ?, ?)`)
	db.getResultPlacementsStmt = conn.Prepare(`
	SELECT group_name, item_player_id, item_name, location_player_id, location_name
	FROM mw_result_placements WHERE rando_id = ?
	ORDER BY location_player_id, location_index`)
	db.sendItemStmt = conn.Prepare("INSERT INTO mw_sent_items (rando_id, sender_id, destination_player_id, label, content, status) VALUES (?, ?, ?, ?, ?, 0)")
	db.getSentItemsStmt = conn.Prepare(`
	SELECT msi.sender_id, mp.nickname, msi.label, msi.content
	FROM mw_sent_items msi
		JOIN mw_players mp ON msi.rando_id = mp.rando_id AND msi.sender_id = mp.player_id
	WHERE msi.rando_id = ? AND msi.destination_player_id = ? AND msi.status < ?`)
	db.confirmItemStmt = conn.Prepare("UPDATE mw_sent_items SET status = 1 WHERE rando_id = ? AND destination_player_id = ? AND sender_id = ? AND label = ? AND content = ?")
	db.markItemsAsSavedStmt = conn.Prepare("UPDATE mw_sent_items SET status = 2 WHERE rando_id = ? AND destination_player_id = ? AND status = 1")
	db.hasNotchCostsStmt = conn.Prepare("SELECT has_notch_costs FROM mw_players WHERE rando_id = ? AND player_id = ?")
	db.markNotchCostsGotStmt = conn.Prepare("UPDATE mw_players SET has_notch_costs = 1 WHERE rando_id = ? AND player_id = ?")
	db.addNotchCostStmt = conn.Prepare("INSERT INTO mw_notch_costs (rando_id, player_id, charm, cost) VALUES (?, ?, ?, ?)")
	// Left join necessary because the player may have submitted an empty notch cost map.
	db.getNotchCostsStmt = conn.Prepare(`
	SELECT mp.player_id, mnc.charm, mnc.cost
	FROM mw_players mp
		LEFT JOIN mw_notch_costs mnc ON mp.rando_id = mnc.rando_id AND mp.player_id = mnc.player_id
	WHERE mp.rando_id = ?1 AND mp.player_id != ?2 AND mp.has_notch_costs AND NOT EXISTS (
		SELECT 1 FROM mw_confirmed_notch_costs mcnc
		WHERE mcnc.rando_id = mp.rando_id AND mcnc.destination_player_id = ?2 AND mcnc.sender_id = mp.player_id
	)`)
	db.confirmNotchCostsStmt = conn.Prepare("INSERT INTO mw_confirmed_notch_costs (rando_id, sender_id, destination_player_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING")
	return db, nil
}

var (
	errRoomNotExist    = errors.New("room does not exist")
	errRoomNotShuffled = errors.New("room is not yet shuffled")
)

const (
	roomStatusOpen = iota
	roomStatusShuffling
	roomStatusShuffled
)

const (
	itemStatusUnconfirmed = iota
	itemStatusConfirmed
	itemStatusSaved
)

type joinedRoom struct {
	randoID  int64
	playerID int64
	status   int
}

func (db *database) joinRoom(roomName string, nickname string) (room joinedRoom, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getRoomInfoByNameStmt
	stmt.BindString(1, roomName)
	err = sqlitex.StepOnce(stmt, func() {
		room.randoID = stmt.ReadInt64(0)
		room.status = stmt.ReadInt32(1)
	})
	if err == sqlitex.ErrZeroRows {
		err = errRoomNotExist
		return
	}
	if !(room.status >= roomStatusOpen && room.status <= roomStatusShuffled) {
		err = fmt.Errorf("unknown room status: %d", room.status)
		return
	}

	stmt = db.checkPlayerStmt
	stmt.BindInt64(1, room.randoID)
	stmt.BindString(2, nickname)
	err = sqlitex.StepOnce(stmt, func() {
		room.playerID = stmt.ReadInt64(0)
	})
	if err != sqlitex.ErrZeroRows {
		return
	}
	stmt = db.addPlayerStmt
	stmt.BindInt64(1, room.randoID)
	stmt.BindString(2, nickname)
	err = sqlitex.StepOnce(stmt, func() {
		room.playerID = stmt.ReadInt64(0)
	})
	return
}

func (db *database) joinShuffledRoom(randoID int32, playerID int32) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.checkRoomJoinableStmt
	defer stmt.Reset()
	stmt.BindInt(1, int(randoID))
	stmt.BindInt(2, int(playerID))
	found, err := stmt.Step()
	if err != nil {
		return err
	}
	if !found {
		return errRoomNotExist
	}
	if stmt.ReadInt32(0) != roomStatusShuffled {
		return errRoomNotShuffled
	}
	return nil
}

func (db *database) sendItem(randoID, selfID, destinationID int64, label, content string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.sendItemStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, selfID)
	stmt.BindInt64(3, destinationID)
	stmt.BindString(4, label)
	stmt.BindString(5, content)
	return sqlitex.Exec(stmt)
}

func (db *database) sendItems(randoID, selfID int64, items []mwproto.Item) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return sqlitex.Transaction(db.conn, func() error {
		stmt := db.sendItemStmt
		for _, item := range items {
			stmt.BindInt64(1, randoID)
			stmt.BindInt64(2, selfID)
			stmt.BindInt(3, int(item.To))
			stmt.BindString(4, item.Label)
			stmt.BindString(5, item.Content)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *database) getItemsWithStatusBelow(randoID, playerID int64, status int) (items []mwproto.DataReceiveMessage, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getSentItemsStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	stmt.BindInt(3, status)

	err = sqlitex.StepAll(stmt, func() {
		items = append(items, mwproto.DataReceiveMessage{
			FromID:  int32(stmt.ReadInt32(0)),
			From:    stmt.ReadString(1),
			Label:   stmt.ReadString(2),
			Content: stmt.ReadString(3),
		})
	})
	return
}

func (db *database) getUnconfirmedItems(randoID, playerID int64) ([]mwproto.DataReceiveMessage, error) {
	return db.getItemsWithStatusBelow(randoID, playerID, itemStatusConfirmed)
}

func (db *database) getUnsavedItems(randoID, playerID int64) ([]mwproto.DataReceiveMessage, error) {
	return db.getItemsWithStatusBelow(randoID, playerID, itemStatusSaved)
}

func (db *database) confirmItem(randoID, playerID int64, item mwproto.DataReceiveConfirmMessage) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	var senderID int64

	stmt := db.checkPlayerStmt
	stmt.BindInt64(1, randoID)
	stmt.BindString(2, item.From)
	err := sqlitex.StepOnce(stmt, func() {
		senderID = stmt.ReadInt64(0)
	})
	if err != nil {
		return err
	}

	stmt = db.confirmItemStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	stmt.BindInt64(3, senderID)
	stmt.BindString(4, item.Label)
	stmt.BindString(5, item.Data)
	return sqlitex.Exec(stmt)
}

func (db *database) markConfirmedItemsSaved(randoID, playerID int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.markItemsAsSavedStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	return sqlitex.Exec(stmt)
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

func (db *database) attachRando(randoID int64, playerID int64, rando mwproto.RandoGeneratedMessage) (err error) {
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
				stmt.BindInt64(1, randoID)
				stmt.BindInt64(2, playerID)
				stmt.BindString(3, groupName)
				stmt.BindInt(4, i)
				stmt.BindString(5, p.Item)
				stmt.BindString(6, p.Location)
				if err := sqlitex.Exec(stmt); err != nil {
					return err
				}
			}
		}

		return nil
	})
}

func (db *database) getAttachedRando(randoID, playerID int64) (rando mwproto.RandoGeneratedMessage, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getPlayerSeedStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	err = sqlitex.StepOnce(stmt, func() {
		rando.Seed = int32(stmt.ReadInt32(0))
	})
	if err != nil {
		return
	}

	rando.Items = map[string][]mwproto.Placement{}
	stmt = db.getPlayerPlacementsStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	err = sqlitex.StepAll(stmt, func() {
		group := stmt.ReadString(0)
		itemName := stmt.ReadString(1)
		locationName := stmt.ReadString(2)

		rando.Items[group] = append(rando.Items[group],
			mwproto.Placement{Item: itemName, Location: locationName})
	})
	return
}

type room struct {
	ID      int64
	Name    string
	Status  int
	Players []player
}

type player struct {
	ID       int64
	Nickname string
	HasSeed  bool
}

func (db *database) getRoomName(id int64) (name string, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getRoomInfoByIDStmt
	stmt.BindInt64(1, id)
	err = sqlitex.StepOnce(stmt, func() {
		name = stmt.ReadString(0)
	})
	return
}

func (db *database) getRoomInfo(name string) (room room, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	room.Name = name

	stmt := db.getRoomInfoByNameStmt
	stmt.BindString(1, name)
	err = sqlitex.StepOnce(stmt, func() {
		room.ID = stmt.ReadInt64(0)
		room.Status = stmt.ReadInt32(1)
	})
	if err == sqlitex.ErrZeroRows {
		err = errRoomNotExist
		return
	}

	stmt = db.listRoomPlayersStmt
	stmt.BindInt64(1, room.ID)

	err = sqlitex.StepAll(stmt, func() {
		room.Players = append(room.Players, player{
			Nickname: stmt.ReadString(0),
			HasSeed:  !stmt.IsNull(1),
		})
	})
	return
}

func (db *database) getAttachedRandos(randoID int64) (worlds []world, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var seeds []int

	stmt := db.listRoomPlayersStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		seeds = append(seeds, stmt.ReadInt32(1))
	})
	if err != nil {
		return
	}
	if len(seeds) == 0 {
		err = errRoomNotExist
		return
	}

	worlds = make([]world, len(seeds))
	for i := range worlds {
		worlds[i].playerID = int64(i)
		worlds[i].placements = map[string][]sphere{}
	}

	stmt = db.getAllPlacementsStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		playerID := stmt.ReadInt64(0)
		groupName := stmt.ReadString(1)
		item := stmt.ReadString(2)
		location := stmt.ReadString(3)
		i := stmt.ReadInt32(4)

		placements := worlds[playerID].placements
		placements[groupName] = append(placements[groupName],
			sphere{placement{Item: item, Location: location, Index: i}})
	})
	return
}

func (db *database) lockRoom(randoID int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.setRoomStatusStmt
	stmt.BindInt(1, roomStatusShuffling)
	stmt.BindInt64(2, randoID)
	return sqlitex.Exec(stmt)
}

func (db *database) saveShuffleResult(randoID int64, placements []mixedPlacement) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return sqlitex.Transaction(db.conn, func() error {
		stmt := db.addResultPlacementStmt
		for _, p := range placements {
			stmt.BindInt64(1, randoID)
			stmt.BindString(2, p.Group)
			stmt.BindInt64(3, p.Item.World)
			stmt.BindString(4, p.Item.Name)
			stmt.BindInt64(5, p.Location.World)
			stmt.BindString(6, p.Location.Name)
			stmt.BindInt(7, p.Location.Index)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		stmt = db.setRoomStatusStmt
		stmt.BindInt(1, roomStatusShuffled)
		stmt.BindInt64(2, randoID)

		return sqlitex.Exec(stmt)
	})
}

func (db *database) getShuffleResult(randoID int64, playerID int64) (result mwproto.ResultMessage, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result.RandoID = int32(randoID)
	result.PlayerID = int32(playerID)

	stmt := db.listRoomPlayersStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		result.Nicknames = append(result.Nicknames, stmt.ReadString(0))
	})
	if err != nil {
		return
	}

	var (
		hasher                = sha256.New224()
		mainSpoilerBuilder    strings.Builder
		playerSpoilerBuilders = make([]strings.Builder, len(result.Nicknames))
		spoilerWriter         = io.MultiWriter(hasher, &mainSpoilerBuilder)
	)

	result.Placements = map[string][]mwproto.ResultPlacement{}
	result.PlayerItemsPlacements = map[string]string{}

	// We need all these slices to be non-nil so that they will be non-null in the JSON
	// encoding.
	result.ReadyMetadata = make([][]mwproto.KeyValuePair, len(result.Nicknames))
	for i := range result.ReadyMetadata {
		result.ReadyMetadata[i] = []mwproto.KeyValuePair{}
	}

	stmt = db.getResultPlacementsStmt
	stmt.BindInt64(1, randoID)
	err = sqlitex.StepAll(stmt, func() {
		group := stmt.ReadString(0)
		itemPID := stmt.ReadInt64(1)
		itemName := stmt.ReadString(2)
		locationPID := stmt.ReadInt64(3)
		locationName := stmt.ReadString(4)

		if locationPID == playerID {
			result.Placements[group] = append(result.Placements[group], mwproto.ResultPlacement{
				Item:     mwproto.QualifyName(int32(itemPID), itemName),
				Location: locationName,
			})
		}

		if itemPID == playerID {
			result.PlayerItemsPlacements[itemName] = mwproto.QualifyName(int32(locationPID), locationName)
		}

		fmt.Fprintf(spoilerWriter, "%s's %s @ %s's %s\n", result.Nicknames[itemPID], itemName, result.Nicknames[locationPID], locationName)
		fmt.Fprintf(&playerSpoilerBuilders[locationPID], "%s's %s @ %s\n", result.Nicknames[itemPID], itemName, locationName)
	})
	if err != nil {
		return
	}

	result.ItemsSpoiler.FullOrderedItemsLog = mainSpoilerBuilder.String()
	result.ItemsSpoiler.IndividualWorldSpoilers = make(map[string]string, len(result.Nicknames))
	for i, name := range result.Nicknames {
		result.ItemsSpoiler.IndividualWorldSpoilers[name] = playerSpoilerBuilders[i].String()
	}

	// this will be a hash of the spoiler log
	hash := hasher.Sum(make([]byte, 0, sha256.Size224))
	result.GeneratedHash = fmt.Sprintf("%02X", hash[:8])
	return
}

func (db *database) hasNotchCosts(randoID, playerID int64) (ok bool, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.hasNotchCostsStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)
	err = sqlitex.StepOnce(stmt, func() {
		ok = stmt.ReadBool(0)
	})
	return
}

func (db *database) saveNotchCosts(randoID, playerID int64, costs map[int]int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	return sqlitex.Transaction(db.conn, func() error {
		stmt := db.addNotchCostStmt

		for charm, cost := range costs {
			stmt.BindInt64(1, randoID)
			stmt.BindInt64(2, playerID)
			stmt.BindInt(3, charm)
			stmt.BindInt(4, cost)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		stmt = db.markNotchCostsGotStmt
		stmt.BindInt64(1, randoID)
		stmt.BindInt64(2, playerID)
		return sqlitex.Exec(stmt)
	})
}

func (db *database) getUnconfirmedNotchCosts(randoID, playerID int64) (costs map[int64]map[int]int, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.getNotchCostsStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, playerID)

	costs = map[int64]map[int]int{}
	err = sqlitex.StepAll(stmt, func() {
		playerID := stmt.ReadInt64(0)
		charm := stmt.ReadInt32(1)
		cost := stmt.ReadInt32(2)
		m, ok := costs[playerID]
		if !ok {
			m = map[int]int{}
			costs[playerID] = m
		}
		if !stmt.IsNull(1) {
			m[charm] = cost
		}
	})
	return
}

func (db *database) confirmNotchCosts(randoID, playerID, senderID int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmt := db.confirmNotchCostsStmt
	stmt.BindInt64(1, randoID)
	stmt.BindInt64(2, senderID)
	stmt.BindInt64(3, playerID)
	return sqlitex.Exec(stmt)
}
