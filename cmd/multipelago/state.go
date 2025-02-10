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
	addUnconfirmedItemStmt     *sqlite.Statement
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

func (ps *persistentState) clearedLocations() (ids []int, err error) {
	// We must never return a nil slice from this method, as it will be sent
	// verbatim to AP clients.
	ids = []int{}
	stmt := ps.selectClearedLocationsStmt
	err = exec(stmt, func() {
		ids = append(ids, stmt.ReadInt32(0))
	})
	return
}

func (ps *persistentState) isLocationCleared(id int) (cleared bool, err error) {
	stmt := ps.isLocationClearedStmt
	stmt.BindInt(1, id)
	err = execOnce(stmt, func() {
		cleared = stmt.ReadInt32(0) == 1
	})
	return
}

func (ps *persistentState) clearLocation(id int) error {
	stmt := ps.addClearedLocationStmt
	defer stmt.Reset()
	stmt.BindInt(1, id)
	if err := stmt.Exec(); err != nil {
		return err
	}
	return stmt.Reset()
}

func (ps *persistentState) addSentItem(item approto.NetworkItem) (index int, err error) {
	stmt := ps.addSentItemStmt
	stmt.BindInt(1, item.Item)
	stmt.BindInt(2, item.Location)
	stmt.BindInt(3, item.Player)
	stmt.BindInt(4, item.Flags)
	err = execOnce(stmt, func() {
		// We rely on the database generating sequential IDs for rows in
		// ap_sent_items. While this is not guaranteed in the general case,
		// the algorithm described in https://www.sqlite.org/autoinc.html
		// does work this way if no rows are ever deleted.
		index = stmt.ReadInt32(0) - 1
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
	ps.addUnconfirmedItemStmt.Close()
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
		addUnconfirmedItemStmt:     db.Prepare("INSERT INTO mw_unconfirmed_sent_items (label, content, dest_player_id) VALUES (?, ?, ?)"),
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
