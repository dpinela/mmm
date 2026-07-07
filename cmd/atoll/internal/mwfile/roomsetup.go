package mwfile

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlitex"
)

type PlayerID int64

func (f *File) Join(nickname string) (playerID PlayerID, playerNames []string, err error) {
	err = sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT player_id FROM mw_players WHERE nickname = ?")
		defer stmt.Close()
		stmt.BindString(1, nickname)
		err = sqlitex.StepOnce(stmt, func() {
			playerID = PlayerID(stmt.ReadInt64(0))
		})
		if err != sqlitex.ErrZeroRows {
			return err
		}

		isShuffled, err := f.isShuffled()
		if err != nil {
			return err
		}
		if isShuffled {
			return ErrRoomAlreadyShuffled
		}

		stmt = f.db.PrepareTemp(`
		INSERT INTO mw_players (player_id, nickname, has_notch_costs)
		VALUES ((SELECT COUNT(*) FROM mw_players), ?1, 0)
		RETURNING player_id`)
		defer stmt.Close()

		stmt.BindString(1, nickname)
		err = sqlitex.StepOnce(stmt, func() {
			playerID = PlayerID(stmt.ReadInt64(0))
		})
		if err != nil {
			return err
		}

		playerNames, err = f.PlayerNames()
		return err
	})
	return
}

func (f *File) Unjoin(playerID PlayerID) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		isShuffled, err := f.isShuffled()
		if err != nil {
			return err
		}
		if isShuffled {
			return ErrRoomAlreadyShuffled
		}

		stmt := f.db.PrepareTemp("DELETE FROM mw_players WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		if err := stmt.Exec(); err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("UPDATE mw_players SET player_id = player_id - 1 WHERE player_id > ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		return stmt.Exec()
	})
}

func (f *File) PlayerNames() (playerNames []string, err error) {
	stmt := f.db.PrepareTemp("SELECT nickname FROM mw_players ORDER BY player_id")
	defer stmt.Close()
	err = sqlitex.StepAll(stmt, func() {
		playerNames = append(playerNames, stmt.ReadString(0))
	})
	return
}

func (f *File) Attach(playerID PlayerID, rando mwproto.RandoGeneratedMessage) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("UPDATE mw_players SET rando_seed = ? WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt(1, int(rando.Seed))
		stmt.BindInt64(2, int64(playerID))
		if err := sqlitex.Exec(stmt); err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("DELETE FROM mw_player_placements WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		if err := sqlitex.Exec(stmt); err != nil {
			return err
		}

		stmt = f.db.PrepareTemp("INSERT INTO mw_player_placements (player_id, group_name, index_, item_name, location_name) VALUES (?, ?, ?, ?, ?)")
		defer stmt.Close()
		for groupName, placements := range rando.Items {
			for i, p := range placements {
				stmt.BindInt64(1, int64(playerID))
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

func (f *File) GetWorld(playerID PlayerID) (rando mwproto.RandoGeneratedMessage, err error) {
	err = sqlitex.Transaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT rando_seed FROM mw_players WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		err = sqlitex.StepOnce(stmt, func() {
			rando.Seed = int32(stmt.ReadInt32(0))
		})
		if err != nil {
			return err
		}
		rando.Items = map[string][]mwproto.Placement{}

		stmt = f.db.PrepareTemp("SELECT group_name, item_name, location_name FROM mw_player_placements WHERE player_id = ? ORDER BY index_")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		err = sqlitex.StepAll(stmt, func() {
			group := stmt.ReadString(0)
			itemName := stmt.ReadString(1)
			locationName := stmt.ReadString(2)

			rando.Items[group] = append(rando.Items[group],
				mwproto.Placement{Item: itemName, Location: locationName})
		})
		return err
	})
	return
}

func (f *File) GetShuffleResult(playerID PlayerID) (result mwproto.ResultMessage, err error) {
	result.PlayerID = int32(playerID)

	var (
		hasher                = sha256.New224()
		mainSpoilerBuilder    strings.Builder
		playerSpoilerBuilders []strings.Builder
		spoilerWriter         = io.MultiWriter(hasher, &mainSpoilerBuilder)
	)

	err = sqlitex.Transaction(f.db, func() error {
		stmt := f.db.PrepareTemp("SELECT nickname FROM mw_players ORDER BY player_id")
		defer stmt.Close()
		err = sqlitex.StepAll(stmt, func() {
			result.Nicknames = append(result.Nicknames, stmt.ReadString(0))
		})
		if err != nil {
			return err
		}

		playerSpoilerBuilders = make([]strings.Builder, len(result.Nicknames))

		result.Placements = map[string][]mwproto.ResultPlacement{}
		result.PlayerItemsPlacements = map[string]string{}

		// We need all these slices to be non-nil so that they will be non-null in the JSON
		// encoding.
		result.ReadyMetadata = make([][]mwproto.KeyValuePair, len(result.Nicknames))
		for i := range result.ReadyMetadata {
			result.ReadyMetadata[i] = []mwproto.KeyValuePair{}
		}

		stmt = f.db.PrepareTemp(`
		SELECT group_name, item_player_id, item_name, location_player_id, location_name
		FROM mw_result_placements
		ORDER BY location_player_id, location_index`)
		defer stmt.Close()

		err = sqlitex.StepAll(stmt, func() {
			group := stmt.ReadString(0)
			itemPID := PlayerID(stmt.ReadInt64(1))
			itemName := stmt.ReadString(2)
			locationPID := PlayerID(stmt.ReadInt64(3))
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
		return err
	})

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

func (f *File) Shuffle() error {
	return sqlitex.WriteTransaction(f.db, func() error {
		worlds, err := f.getAttachedWorlds()
		if err != nil {
			return err
		}
		result := mix(worlds)
		return f.saveShuffleResult(result)
	})
}

func (f *File) getAttachedWorlds() (worlds []world, err error) {
	stmt := f.db.PrepareTemp("SELECT rando_seed FROM mw_players ORDER BY player_id")
	defer stmt.Close()

	err = sqlitex.StepAll(stmt, func() {
		worlds = append(worlds, world{seed: stmt.ReadInt64(0)})
	})
	if err != nil {
		return
	}
	if len(worlds) == 0 {
		err = ErrRoomEmpty
		return
	}

	for i := range worlds {
		worlds[i].playerID = int64(i)
		worlds[i].placements = map[string][]sphere{}
	}

	stmt = f.db.PrepareTemp("SELECT player_id, group_name, item_name, location_name, index_ FROM mw_player_placements ORDER BY player_id, index_")
	defer stmt.Close()

	err = sqlitex.StepAll(stmt, func() {
		playerID := stmt.ReadInt64(0)
		groupName := stmt.ReadString(1)
		ps := worlds[playerID].placements
		ps[groupName] = append(ps[groupName], sphere{
			{
				Item:     stmt.ReadString(2),
				Location: stmt.ReadString(3),
				Index:    stmt.ReadInt32(4),
			},
		})
	})
	return
}

func (f *File) saveShuffleResult(placements []mixedPlacement) error {
	stmt := f.db.PrepareTemp("INSERT INTO mw_result_placements (group_name, item_player_id, item_name, location_player_id, location_name, location_index) VALUES (?, ?, ?, ?, ?, ?)")
	defer stmt.Close()

	for _, p := range placements {
		stmt.BindString(1, p.Group)
		stmt.BindInt64(2, p.Item.World)
		stmt.BindString(3, p.Item.Name)
		stmt.BindInt64(4, p.Location.World)
		stmt.BindString(5, p.Location.Name)
		stmt.BindInt(6, p.Location.Index)
		if err := sqlitex.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

var (
	ErrRoomEmpty           = errors.New("cannot shuffle empty room")
	ErrRoomAlreadyShuffled = errors.New("room already shuffled")
)

func (f *File) IsShuffled() (shuffled bool, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		shuffled, err = f.isShuffled()
		return err
	})
	return
}

func (f *File) isShuffled() (shuffled bool, err error) {
	stmt := f.db.PrepareTemp("SELECT EXISTS(SELECT 1 FROM mw_result_placements)")
	defer stmt.Close()

	err = sqlitex.StepOnce(stmt, func() {
		shuffled = stmt.ReadBool(0)
	})
	return
}

type Player struct {
	ID       int64
	Nickname string
	HasSeed  bool
}

func (f *File) GetPlayers() (players []Player, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp("SELECT player_id, nickname, rando_seed FROM mw_players ORDER BY player_id")
		defer stmt.Close()

		return sqlitex.StepAll(stmt, func() {
			players = append(players, Player{
				ID:       stmt.ReadInt64(0),
				Nickname: stmt.ReadString(1),
				HasSeed:  !stmt.IsNull(2),
			})
		})
	})
	return
}
