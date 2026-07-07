package mwfile

import "github.com/dpinela/mmm/internal/sqlitex"

func (f *File) HasNotchCosts(playerID PlayerID) (has bool, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp("SELECT has_notch_costs FROM mw_players WHERE player_id = ?")
		defer stmt.Close()

		stmt.BindInt64(1, int64(playerID))

		return sqlitex.StepOnce(stmt, func() {
			has = stmt.ReadBool(0)
		})
	})
	return
}

func (f *File) SaveNotchCosts(playerID PlayerID, costs map[int]int) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("INSERT INTO mw_notch_costs (player_id, charm, cost) VALUES (?, ?, ?)")
		defer stmt.Close()

		for charm, cost := range costs {
			stmt.BindInt64(1, int64(playerID))
			stmt.BindInt(2, charm)
			stmt.BindInt(3, cost)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		stmt = f.db.PrepareTemp("UPDATE mw_players SET has_notch_costs = 1 WHERE player_id = ?")
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		return stmt.Exec()
	})
}

func (f *File) ConfirmNotchCosts(fromPlayerID, toPlayerID PlayerID) error {
	return sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp("INSERT INTO mw_confirmed_notch_costs (sender_id, destination_player_id) VALUES (?, ?) ON CONFLICT DO NOTHING")
		defer stmt.Close()

		stmt.BindInt64(1, int64(fromPlayerID))
		stmt.BindInt64(2, int64(toPlayerID))
		return stmt.Exec()
	})
}

func (f *File) GetUnconfirmedNotchCosts(playerID PlayerID) (costs map[PlayerID]map[int]int, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		// Left join necessary because the player may have submitted an empty notch cost map.
		stmt := f.db.PrepareTemp(`
		SELECT mp.player_id, mnc.charm, mnc.cost
		FROM mw_players mp
			LEFT JOIN mw_notch_costs mnc USING (player_id)
		WHERE mp.player_id != ?1 AND mp.has_notch_costs AND NOT EXISTS (
			SELECT 1 FROM mw_confirmed_notch_costs mcnc
			WHERE mcnc.destination_player_id = ?1 AND mcnc.sender_id = mp.player_id
		)`)
		defer stmt.Close()

		stmt.BindInt64(1, int64(playerID))

		costs = map[PlayerID]map[int]int{}
		return sqlitex.StepAll(stmt, func() {
			playerID := PlayerID(stmt.ReadInt64(0))
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
	})
	return
}
