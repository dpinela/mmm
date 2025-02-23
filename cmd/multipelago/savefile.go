package main

import (
	"errors"
	"fmt"
	"iter"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlite"
)

type savefile struct {
	db                         *sqlite.DB
	selectClearedLocationsStmt *sqlite.Statement
	addClearedLocationStmt     *sqlite.Statement
	isLocationClearedStmt      *sqlite.Statement
	addSentItemStmt            *sqlite.Statement
	getSentItemsStmt           *sqlite.Statement
	getUnconfirmedItemsStmt    *sqlite.Statement
	addUnconfirmedItemStmt     *sqlite.Statement
	confirmItemStmt            *sqlite.Statement
	addReceivedItemStmt        *sqlite.Statement
	hasReceivedItemStmt        *sqlite.Statement
	getStoredDataStmt          *sqlite.Statement
	setStoredDataStmt          *sqlite.Statement
	getLocationOfOwnItemStmt   *sqlite.Statement
	getPlacedItemStmt          *sqlite.Statement
}

func exec(stmt *sqlite.Statement, rowHandler func()) error {
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

var (
	errZeroRows     = errors.New("statement returned no rows")
	errMultipleRows = errors.New("statement returned multiple rows")
)

func execOnce(stmt *sqlite.Statement, rowHandler func()) error {
	defer stmt.Reset()
	hasRow, err := stmt.Step()
	if err != nil {
		return err
	}
	if !hasRow {
		return errZeroRows
	}
	rowHandler()
	hasRow, err = stmt.Step()
	if err != nil {
		return err
	}
	if hasRow {
		return errMultipleRows
	}
	return stmt.Reset()
}

func (ps *savefile) getNicknames() (names []string, err error) {
	stmt := ps.db.Prepare("SELECT nickname FROM mw_players ORDER BY player_id")
	defer stmt.Close()
	err = exec(stmt, func() {
		names = append(names, stmt.ReadString(0))
	})
	return
}

func (ps *savefile) getConnectionParams() (playerID, randoID int, err error) {
	stmt := ps.db.Prepare("SELECT player_id, rando_id FROM mw_global_data")
	defer stmt.Close()
	err = execOnce(stmt, func() {
		playerID = stmt.ReadInt32(0)
		randoID = stmt.ReadInt32(1)
	})
	return
}

func (ps *savefile) getOwnItemLocations() iter.Seq2[string, error] {
	stmt := ps.db.Prepare("SELECT location_name FROM mw_own_item_placements ORDER BY location_name")
	return execIter(stmt, func() string { return stmt.ReadString(0) })
}

func (ps *savefile) getOwnWorldPlacements() iter.Seq2[ownPlacement, error] {
	stmt := ps.db.Prepare("SELECT ap_location_id, item_name, dest_player_id FROM mw_own_world_placements ORDER BY ap_location_id")
	return execIter(stmt, func() ownPlacement {
		return ownPlacement{
			apLocationID: stmt.ReadInt64(0),
			placedItem:   placedItem{name: stmt.ReadString(1), ownerID: stmt.ReadInt32(2)},
		}
	})
}

func (ps *savefile) getLocationOfOwnItem(itemName string) (locName string, err error) {
	stmt := ps.getLocationOfOwnItemStmt
	stmt.BindString(1, itemName)
	err = execOnce(stmt, func() {
		locName = stmt.ReadString(0)
	})
	return
}

func (ps *savefile) getPlacedItem(locID int64) (item placedItem, err error) {
	stmt := ps.getPlacedItemStmt
	stmt.BindInt64(1, locID)
	err = execOnce(stmt, func() {
		item.name = stmt.ReadString(0)
		item.ownerID = stmt.ReadInt32(1)
	})
	return
}

func execIter[T any](stmt *sqlite.Statement, f func() T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		defer stmt.Close()
		defer stmt.Reset()
		for {
			hasRow, err := stmt.Step()
			if err != nil {
				stmt.Reset()
				yield(zero, err)
				return
			}
			if !hasRow {
				return
			}
			ok := yield(f(), nil)
			if !ok {
				return
			}
		}
	}
}

type ownPlacement struct {
	apLocationID int64
	placedItem
}

func (ps *savefile) clearedLocations() (ids []int64, err error) {
	// We must never return a nil slice from this method, as it will be sent
	// verbatim to AP clients.
	ids = []int64{}
	stmt := ps.selectClearedLocationsStmt
	err = exec(stmt, func() {
		ids = append(ids, stmt.ReadInt64(0))
	})
	return
}

func (ps *savefile) isLocationCleared(id int64) (cleared bool, err error) {
	stmt := ps.isLocationClearedStmt
	stmt.BindInt64(1, id)
	err = execOnce(stmt, func() {
		cleared = stmt.ReadInt32(0) == 1
	})
	return
}

func (ps *savefile) clearLocation(id int64) error {
	stmt := ps.addClearedLocationStmt
	defer stmt.Reset()
	stmt.BindInt64(1, id)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *savefile) addSentItem(item approto.NetworkItem) (index int, err error) {
	stmt := ps.addSentItemStmt
	stmt.BindInt64(1, item.Item)
	stmt.BindInt64(2, item.Location)
	stmt.BindInt(3, item.Player)
	stmt.BindInt(4, item.Flags)
	err = execOnce(stmt, func() {
		// We rely on the database generating sequential IDs for rows in
		// ap_sent_items. While this is not guaranteed in the general case,
		// the algorithm described in https://www.sqlite.org/autoinc.html
		// does work this way if no rows are ever deleted and no conflicts
		// occur.
		index = stmt.ReadInt32(0) - 1
	})
	return
}

func (ps *savefile) getSentItems() (items []approto.NetworkItem, err error) {
	stmt := ps.getSentItemsStmt
	// This will be sent verbatim to AP clients.
	items = []approto.NetworkItem{}
	err = exec(stmt, func() {
		item := approto.NetworkItem{
			Item:     stmt.ReadInt64(0),
			Location: stmt.ReadInt64(1),
			Player:   stmt.ReadInt32(2),
			Flags:    stmt.ReadInt32(3),
		}
		items = append(items, item)
	})
	return
}

func (ps *savefile) getUnconfirmedItems() (items []mwproto.DataSendMessage, err error) {
	stmt := ps.getUnconfirmedItemsStmt
	defer stmt.Reset()
	err = exec(stmt, func() {
		item := mwproto.DataSendMessage{
			Label:   stmt.ReadString(0),
			Content: stmt.ReadString(1),
			To:      int32(stmt.ReadInt32(2)),
			TTL:     sentItemTTL,
		}
		items = append(items, item)
	})
	return
}

func (ps *savefile) addUnconfirmedItem(item mwproto.DataSendMessage) error {
	stmt := ps.addUnconfirmedItemStmt
	defer stmt.Reset()
	stmt.BindString(1, item.Label)
	stmt.BindString(2, item.Content)
	stmt.BindInt(3, int(item.To))
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *savefile) confirmItem(item mwproto.DataSendConfirmMessage) (bool, error) {
	stmt := ps.confirmItemStmt
	stmt.BindString(1, item.Label)
	stmt.BindString(2, item.Content)
	stmt.BindInt(3, int(item.To))
	if err := stmt.Exec(); err != nil {
		return false, err
	}
	if err := stmt.Reset(); err != nil {
		return false, err
	}
	return ps.db.NumChanges() > 0, nil
}

func (ps *savefile) addReceivedItem(label, content string) error {
	stmt := ps.addReceivedItemStmt
	defer stmt.Reset()
	stmt.BindString(1, label)
	stmt.BindString(2, content)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *savefile) hasReceivedItem(label, content string) (received bool, err error) {
	stmt := ps.hasReceivedItemStmt
	stmt.BindString(1, label)
	stmt.BindString(2, content)
	err = execOnce(stmt, func() {
		received = stmt.ReadInt32(0) == 1
	})
	return
}

func (ps *savefile) getStoredData(key string) (data []byte, found bool, err error) {
	stmt := ps.getStoredDataStmt
	defer stmt.Reset()
	stmt.BindString(1, key)
	hasRow, err := stmt.Step()
	if err != nil {
		return
	}
	if !hasRow {
		found = false
		return
	}
	data = stmt.ReadBytes(0)
	found = true
	hasRow, err = stmt.Step()
	if err != nil {
		return
	}
	if hasRow {
		err = errMultipleRows
		return
	}
	err = stmt.Reset()
	return
}

func (ps *savefile) setStoredData(key string, data []byte) error {
	stmt := ps.setStoredDataStmt
	defer stmt.Reset()
	stmt.BindString(1, key)
	stmt.BindBytes(2, data)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *savefile) close() {
	ps.db.Close()
}

func openSavefile(loc string) (*savefile, error) {
	db, err := sqlite.Open(loc)
	if err != nil {
		return nil, fmt.Errorf("open savefile: %w", err)
	}
	return &savefile{
		db:                         db,
		selectClearedLocationsStmt: db.Prepare("SELECT location_id FROM locations_cleared ORDER BY location_id"),
		addClearedLocationStmt:     db.Prepare("INSERT INTO locations_cleared (location_id) VALUES (?)"),
		isLocationClearedStmt:      db.Prepare("SELECT EXISTS(SELECT 1 FROM locations_cleared WHERE location_id = ?)"),
		addSentItemStmt:            db.Prepare("INSERT INTO ap_sent_items (item_id, location_id, player_id, flags) VALUES (?, ?, ?, ?) RETURNING item_index"),
		getSentItemsStmt:           db.Prepare("SELECT item_id, location_id, player_id, flags FROM ap_sent_items ORDER BY item_index"),
		getUnconfirmedItemsStmt:    db.Prepare("SELECT label, content, dest_player_id FROM mw_unconfirmed_sent_items"),
		addUnconfirmedItemStmt:     db.Prepare("INSERT INTO mw_unconfirmed_sent_items (label, content, dest_player_id) VALUES (?, ?, ?)"),
		confirmItemStmt:            db.Prepare("DELETE FROM mw_unconfirmed_sent_items WHERE label = ? AND content = ? AND dest_player_id = ?"),
		addReceivedItemStmt:        db.Prepare("INSERT INTO mw_received_items (label, content) VALUES (?, ?)"),
		hasReceivedItemStmt:        db.Prepare("SELECT EXISTS(SELECT 1 FROM mw_received_items WHERE label = ? AND content = ?)"),
		getStoredDataStmt:          db.Prepare("SELECT json_value FROM ap_data_storage WHERE key = ?"),
		setStoredDataStmt:          db.Prepare("INSERT INTO ap_data_storage (key, json_value) VALUES (?, ?) ON CONFLICT DO UPDATE SET json_value = excluded.json_value"),
		getLocationOfOwnItemStmt:   db.Prepare("SELECT location_name FROM mw_own_item_placements WHERE item_name = ?"),
		getPlacedItemStmt:          db.Prepare("SELECT item_name, dest_player_id FROM mw_own_world_placements WHERE ap_location_id = ?"),
	}, nil
}

func createSavefile(loc string, result mwproto.ResultMessage) error {
	db, err := sqlite.Open(loc)
	if err != nil {
		return err
	}
	defer db.Close()

	const savefileSchema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS locations_cleared (
	location_id INTEGER NOT NULL PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS mw_unconfirmed_sent_items (
	label TEXT NOT NULL,
	content TEXT NOT NULL,
	dest_player_id INTEGER NOT NULL,

	PRIMARY KEY (label, content, dest_player_id)
);

CREATE TABLE IF NOT EXISTS mw_received_items (
	label TEXT NOT NULL,
	content TEXT NOT NULL,

	PRIMARY KEY (label, content)
);

CREATE TABLE IF NOT EXISTS ap_sent_items (
	item_index INTEGER NOT NULL PRIMARY KEY,
	item_id INTEGER NOT NULL,
	location_id INTEGER NOT NULL,
	player_id INTEGER NOT NULL,
	flags INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ap_data_storage (
	key TEXT NOT NULL,
	json_value TEXT NOT NULL,

	PRIMARY KEY (key)
);

CREATE TABLE IF NOT EXISTS mw_players (
	player_id INTEGER NOT NULL PRIMARY KEY,
	nickname TEXT NOT NULL,
	spoiler_log TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mw_global_data (
	player_id INTEGER NOT NULL REFERENCES mw_players (player_id),
	rando_id INTEGER NOT NULL,
	full_spoiler_log TEXT NOT NULL,
	hash TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mw_own_world_placements (
	ap_location_id INTEGER NOT NULL PRIMARY KEY,
	dest_player_id INTEGER NOT NULL REFERENCES mw_players (player_id),
	item_name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mw_own_item_placements (
	item_name TEXT NOT NULL PRIMARY KEY,
	location_name TEXT NOT NULL
);

BEGIN;
`
	if err := db.Exec(savefileSchema); err != nil {
		return err
	}

	stmt := db.Prepare("INSERT INTO mw_players (player_id, nickname, spoiler_log) VALUES (?, ?, ?)")
	for i, name := range result.Nicknames {
		stmt.BindInt(1, i)
		stmt.BindString(2, name)
		stmt.BindString(3, result.ItemsSpoiler.IndividualWorldSpoilers[name])
		if err := stmt.Exec(); err != nil {
			return err
		}
		if err := stmt.Reset(); err != nil {
			return err
		}
	}
	stmt.Close()

	stmt = db.Prepare("INSERT INTO mw_global_data (player_id, rando_id, full_spoiler_log, hash) VALUES (?, ?, ?, ?)")
	stmt.BindInt(1, int(result.PlayerID))
	stmt.BindInt(2, int(result.RandoID))
	stmt.BindString(3, result.ItemsSpoiler.FullOrderedItemsLog)
	stmt.BindString(4, result.GeneratedHash)
	if err := stmt.Exec(); err != nil {
		return err
	}
	if err := stmt.Reset(); err != nil {
		return err
	}
	stmt.Close()

	stmt = db.Prepare("INSERT INTO mw_own_world_placements (ap_location_id, dest_player_id, item_name) VALUES (?, ?, ?)")
	for _, p := range result.Placements[singularItemGroup] {
		locID, ok := mwproto.ParseDiscriminator(p.Location)
		if !ok {
			return fmt.Errorf("location without discriminator: %s", p.Location)
		}
		pid, item, ok := mwproto.ParseQualifiedName(p.Item)
		if !ok {
			return fmt.Errorf("item without qualifier: %s", p.Item)
		}
		stmt.BindInt64(1, locID)
		stmt.BindInt(2, pid)
		stmt.BindString(3, item)
		if err := stmt.Exec(); err != nil {
			return err
		}
		if err := stmt.Reset(); err != nil {
			return err
		}
	}
	stmt.Close()

	stmt = db.Prepare("INSERT INTO mw_own_item_placements (item_name, location_name) VALUES (?, ?)")
	for item, loc := range result.PlayerItemsPlacements {
		stmt.BindString(1, item)
		stmt.BindString(2, loc)
		if err := stmt.Exec(); err != nil {
			return err
		}
		if err := stmt.Reset(); err != nil {
			return err
		}
	}
	stmt.Close()

	return db.Exec("COMMIT")
}
