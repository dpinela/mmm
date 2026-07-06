package mwfile

import (
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlitex"
)

const (
	itemStatusUnconfirmed = iota
	itemStatusConfirmed
	itemStatusSaved
)

func (f *File) GetUnsavedItems(playerID PlayerID) ([]mwproto.DataReceiveMessage, error) {
	return f.getItemsWithStatusBelow(playerID, itemStatusSaved)
}

func (f *File) GetUnconfirmedItems(playerID PlayerID) ([]mwproto.DataReceiveMessage, error) {
	return f.getItemsWithStatusBelow(playerID, itemStatusConfirmed)
}

func (f *File) getItemsWithStatusBelow(playerID PlayerID, status int) (items []mwproto.DataReceiveMessage, err error) {
	stmt := f.db.PrepareTemp(`
	SELECT msi.sender_id, mp.nickname, msi.label, msi.content
	FROM mw_sent_items msi
	JOIN mw_players mp ON msi.sender_id = mp.player_id WHERE msi.destination_player_id = ? AND msi.status < ?`)
	defer stmt.Close()

	stmt.BindInt64(1, int64(playerID))
	stmt.BindInt(2, status)

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

func (f *File) SendItems(fromPlayerID PlayerID, items ...mwproto.Item) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("INSERT INTO mw_sent_items (sender_id, destination_player_id, label, content, status) VALUES (?, ?, ?, ?, 0)")
		defer stmt.Close()

		for _, item := range items {
			stmt.BindInt64(1, int64(fromPlayerID))
			stmt.BindInt64(2, int64(item.To))
			stmt.BindString(3, item.Label)
			stmt.BindString(4, item.Content)
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f *File) ConfirmItem(toPlayerID PlayerID, item mwproto.DataReceiveConfirmMessage) error {
	stmt := f.db.PrepareTemp(`
	UPDATE mw_sent_items
	SET status = 1
	WHERE sender_id = (SELECT player_id FROM mw_players WHERE nickname = ?)
	AND destination_player_id = ? AND label = ? AND content = ? AND status < 2`)
	defer stmt.Close()

	stmt.BindString(1, item.From)
	stmt.BindInt64(2, int64(toPlayerID))
	stmt.BindString(3, item.Label)
	stmt.BindString(4, item.Data)
	return stmt.Exec()
}

func (f *File) SaveConfirmedItems(playerID PlayerID) error {
	stmt := f.db.PrepareTemp("UPDATE mw_sent_items SET status = 2 WHERE destination_player_id = ? AND status = 1")
	defer stmt.Close()

	stmt.BindInt64(1, int64(playerID))
	return stmt.Exec()
}
