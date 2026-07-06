package mwfile

import (
	"github.com/dpinela/mmm/internal/sqlite"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type File struct {
	db *sqlite.DB
}

func Open(filename string) (*File, error) {
	const schema = `
	PRAGMA journal_mode = WAL;

	CREATE TABLE IF NOT EXISTS mw_players (
		player_id INTEGER PRIMARY KEY CHECK (player_id >= 0),
		nickname TEXT NOT NULL UNIQUE,
		rando_seed INTEGER,
		has_notch_costs INTEGER NOT NULL
	) STRICT;
	
	CREATE TABLE IF NOT EXISTS mw_player_placements (
		player_id INTEGER NOT NULL,
		group_name TEXT NOT NULL,
		index_ INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		location_name TEXT NOT NULL,

		FOREIGN KEY (player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,

		PRIMARY KEY (player_id, group_name, index_)
	) STRICT;
	
	CREATE TABLE IF NOT EXISTS mw_ready_metadata (
		player_id INTEGER NOT NULL,
		index_ INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,

		FOREIGN KEY (player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,

		PRIMARY KEY (player_id, index_)
	) STRICT;
	
	CREATE TABLE IF NOT EXISTS mw_result_placements (
		item_player_id INTEGER NOT NULL,
		item_name TEXT NOT NULL,
		group_name TEXT NOT NULL,
		location_player_id INTEGER NOT NULL,
		location_name TEXT NOT NULL,
		location_index INTEGER NOT NULL,

		FOREIGN KEY (item_player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,
		FOREIGN KEY (location_player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE
	) STRICT;
	
	CREATE TABLE IF NOT EXISTS mw_sent_items (
		sender_id INTEGER NOT NULL,
		destination_player_id INTEGER NOT NULL,
		label TEXT NOT NULL,
		content TEXT NOT NULL,
		status INTEGER NOT NULL,

		PRIMARY KEY (sender_id, destination_player_id, label, content),

		FOREIGN KEY (sender_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,
		FOREIGN KEY (destination_player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE
	) STRICT;

	CREATE INDEX IF NOT EXISTS unconfirmed_items_by_recipient ON mw_sent_items (destination_player_id);

	CREATE TABLE IF NOT EXISTS mw_notch_costs (
		player_id INTEGER NOT NULL,
		charm INTEGER NOT NULL,
		cost INTEGER NOT NULL,

		FOREIGN KEY (player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,

		PRIMARY KEY (player_id, charm)
	) STRICT;

	CREATE TABLE IF NOT EXISTS mw_confirmed_notch_costs (
		sender_id INTEGER NOT NULL,
		destination_player_id INTEGER NOT NULL,

		FOREIGN KEY (sender_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,
		FOREIGN KEY (destination_player_id) REFERENCES mw_players ON DELETE CASCADE ON UPDATE CASCADE,

		PRIMARY KEY (sender_id, destination_player_id)
	) STRICT
	`

	db, err := sqlitex.OpenWithSchema(filename, schema)
	if err != nil {
		return nil, err
	}
	return &File{db}, nil
}

func (f *File) Close() {
	f.db.Close()
}
