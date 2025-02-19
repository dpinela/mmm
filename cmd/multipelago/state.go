package main

import (
	"errors"
	"fmt"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlite"
)

type persistentState struct {
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

func (ps *persistentState) clearedLocations() (ids []int64, err error) {
	// We must never return a nil slice from this method, as it will be sent
	// verbatim to AP clients.
	ids = []int64{}
	stmt := ps.selectClearedLocationsStmt
	err = exec(stmt, func() {
		ids = append(ids, stmt.ReadInt64(0))
	})
	return
}

func (ps *persistentState) isLocationCleared(id int64) (cleared bool, err error) {
	stmt := ps.isLocationClearedStmt
	stmt.BindInt64(1, id)
	err = execOnce(stmt, func() {
		cleared = stmt.ReadInt32(0) == 1
	})
	return
}

func (ps *persistentState) clearLocation(id int64) error {
	stmt := ps.addClearedLocationStmt
	defer stmt.Reset()
	stmt.BindInt64(1, id)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *persistentState) addSentItem(item approto.NetworkItem) (index int, err error) {
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

func (ps *persistentState) getSentItems() (items []approto.NetworkItem, err error) {
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

func (ps *persistentState) getUnconfirmedItems() (items []mwproto.DataSendMessage, err error) {
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

func (ps *persistentState) addUnconfirmedItem(item mwproto.DataSendMessage) error {
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

func (ps *persistentState) confirmItem(item mwproto.DataSendConfirmMessage) (bool, error) {
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

func (ps *persistentState) addReceivedItem(item mwproto.DataReceiveMessage) error {
	stmt := ps.addReceivedItemStmt
	defer stmt.Reset()
	stmt.BindString(1, item.Label)
	stmt.BindString(2, item.Content)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *persistentState) hasReceivedItem(item mwproto.DataReceiveMessage) (received bool, err error) {
	stmt := ps.hasReceivedItemStmt
	stmt.BindString(1, item.Label)
	stmt.BindString(2, item.Content)
	err = execOnce(stmt, func() {
		received = stmt.ReadInt32(0) == 1
	})
	return
}

func (ps *persistentState) getStoredData(key string) (data []byte, found bool, err error) {
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

func (ps *persistentState) setStoredData(key string, data []byte) error {
	stmt := ps.setStoredDataStmt
	defer stmt.Reset()
	stmt.BindString(1, key)
	stmt.BindBytes(2, data)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *persistentState) close() {
	ps.selectClearedLocationsStmt.Close()
	ps.isLocationClearedStmt.Close()
	ps.addClearedLocationStmt.Close()
	ps.addSentItemStmt.Close()
	ps.getSentItemsStmt.Close()
	ps.getUnconfirmedItemsStmt.Close()
	ps.addUnconfirmedItemStmt.Close()
	ps.confirmItemStmt.Close()
	ps.addReceivedItemStmt.Close()
	ps.hasReceivedItemStmt.Close()
	ps.getStoredDataStmt.Close()
	ps.setStoredDataStmt.Close()
	ps.db.Close()
}

func openPersistentState(loc string) (*persistentState, error) {
	db, err := sqlite.Open(loc)
	if err != nil {
		return nil, fmt.Errorf("open persistent state file: %w", err)
	}
	if err := db.Exec(persistentStateSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init persistent state file: %w", err)
	}
	return &persistentState{
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
	}, nil
}

const persistentStateSchema = `
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
)
`
