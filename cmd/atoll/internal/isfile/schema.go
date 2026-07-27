package isfile

import (
	"github.com/dpinela/mmm/internal/sqlite"
)

type File struct {
	db *sqlite.DB
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS is_globals (
	hash INTEGER,
	has_settings INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS is_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS is_players (
	player_id INTEGER PRIMARY KEY CHECK (player_id >= 0),
	nickname TEXT NOT NULL UNIQUE
) STRICT;

CREATE TABLE IF NOT EXISTS is_ready_metadata (
	player_id INTEGER NOT NULL,
	index_ INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,

	FOREIGN KEY (player_id) REFERENCES is_players ON DELETE CASCADE ON UPDATE CASCADE,

	PRIMARY KEY (player_id, index_)
) STRICT;

CREATE TABLE IF NOT EXISTS is_sent_items (
	item_id INTEGER PRIMARY KEY,
	label TEXT NOT NULL,
	content TEXT NOT NULL,
	source_player_id INTEGER NOT NULL REFERENCES is_players ON DELETE CASCADE ON UPDATE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS is_sent_item_statuses (
	item_id INTEGER NOT NULL REFERENCES is_sent_items,
	destination_player_id INTEGER NOT NULL REFERENCES is_players ON DELETE CASCADE ON UPDATE CASCADE,
	status INTEGER NOT NULL,

	PRIMARY KEY (destination_player_id, item_id)
) STRICT;
`

func Open(filename string) (*File, error) {
	db, err := sqlite.Open(filename)
	if err != nil {
		return nil, err
	}
	if err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &File{db}, nil
}

func (f *File) Close() {
	f.db.Close()
}

type PlayerID int64
