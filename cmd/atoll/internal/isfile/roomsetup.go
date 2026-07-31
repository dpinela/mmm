package isfile

import (
	"encoding/json"
	"fmt"

	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type HashMismatchError struct {
	existing int
	new      int
}

func (err HashMismatchError) Error() string {
	return fmt.Sprintf("hash mismatch: existing hash is %d, new is %d", err.existing, err.new)
}

func (f *File) Join(nickname string, hash int, metadata []mwproto.KeyValuePair) (playerID PlayerID, playerNames []string, err error) {

	err = sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT hash FROM is_globals")
		defer stmt.Close()

		var existingHash int
		err := sqlitex.StepOnce(stmt, func() {
			existingHash = stmt.ReadInt32(0)
		})
		switch err {
		case sqlitex.ErrZeroRows:
			stmt = f.db.PrepareTemp("INSERT INTO is_globals (hash, has_settings) VALUES (?, 0)")
			defer stmt.Close()
			stmt.BindInt(1, hash)
			if err = stmt.Exec(); err != nil {
				return err
			}
		case nil:
			if existingHash != hash {
				return HashMismatchError{existingHash, hash}
			}
		default:
			return err
		}

		stmt = f.db.PrepareTemp("SELECT player_id FROM is_players WHERE nickname = ?")
		defer stmt.Close()
		stmt.BindString(1, nickname)
		err = sqlitex.StepOnce(stmt, func() {
			playerID = PlayerID(stmt.ReadInt64(0))
		})
		if err == sqlitex.ErrZeroRows {
			stmt = f.db.PrepareTemp("INSERT INTO is_players (player_id, nickname) VALUES ((SELECT COUNT(*) FROM is_players), ?) RETURNING player_id")
			defer stmt.Close()
			stmt.BindString(1, nickname)
			err = sqlitex.StepOnce(stmt, func() {
				playerID = PlayerID(stmt.ReadInt64(0))
			})
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("DELETE FROM is_ready_metadata WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		if err = stmt.Exec(); err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("INSERT INTO is_ready_metadata (player_id, index_, key, value) VALUES (?, ?, ?, ?)")
		defer stmt.Close()
		for i, pair := range metadata {
			stmt.BindInt64(1, int64(playerID))
			stmt.BindInt(2, i)
			stmt.BindString(3, pair.Key)
			stmt.BindString(4, pair.Value)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		playerNames, err = f.playerNames()
		return err
	})
	return
}

func (f *File) Unjoin(playerID PlayerID) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("DELETE FROM is_players WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		if err := stmt.Exec(); err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("UPDATE is_players SET player_id = player_id - 1 WHERE player_id > ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		return stmt.Exec()
	})
}

func (f *File) GetGlobalSettings() (settings map[string]json.RawMessage, err error) {
	err = sqlitex.Transaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT has_settings FROM is_globals")
		defer stmt.Close()
		var hasSettings bool
		err = sqlitex.StepOnce(stmt, func() {
			hasSettings = stmt.ReadBool(0)
		})
		if err != nil || !hasSettings {
			return err
		}

		stmt = f.db.PrepareTemp("SELECT key, value FROM is_settings")
		defer stmt.Close()
		settings = map[string]json.RawMessage{}
		err = sqlitex.StepAll(stmt, func() {
			settings[stmt.ReadString(0)] = stmt.ReadBytes(1)
		})
		if err == sqlitex.ErrZeroRows {
			err = nil
		}
		return err
	})
	return
}

func (f *File) SetGlobalSettings(settings map[string]json.RawMessage) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("INSERT INTO is_settings (key, value) VALUES (?, ?)")
		defer stmt.Close()
		for k, v := range settings {
			stmt.BindString(1, k)
			stmt.BindBytes(2, v)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		stmt = f.db.PrepareTemp("UPDATE is_globals SET has_settings = 1")
		defer stmt.Close()
		return stmt.Exec()
	})
}

func (f *File) playerNames() (playerNames []string, err error) {
	stmt := f.db.PrepareTemp("SELECT nickname FROM is_players ORDER BY player_id")
	defer stmt.Close()
	err = sqlitex.StepAll(stmt, func() {
		playerNames = append(playerNames, stmt.ReadString(0))
	})
	return
}

func (f *File) PlayerNames() (playerNames []string, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		playerNames, err = f.playerNames()
		return err
	})
	return
}

func (f *File) GetFinalPlayers() (nicknames []string, readyMetadata [][]mwproto.KeyValuePair, err error) {
	err = sqlitex.Transaction(f.db, func() error {
		nicknames, err = f.playerNames()
		if err != nil {
			return err
		}
		readyMetadata = make([][]mwproto.KeyValuePair, len(nicknames))

		for i := range readyMetadata {
			// We must not return a nil slice here as it will be returned
			// verbatim to MW clients.
			readyMetadata[i] = []mwproto.KeyValuePair{}
		}

		stmt := f.db.PrepareTemp("SELECT player_id, key, value FROM is_ready_metadata ORDER BY player_id, index_")
		defer stmt.Close()
		return sqlitex.StepAll(stmt, func() {
			playerID := stmt.ReadInt64(0)
			readyMetadata[playerID] = append(readyMetadata[playerID], mwproto.KeyValuePair{
				Key:   stmt.ReadString(1),
				Value: stmt.ReadString(2),
			})
		})
	})
	return
}
