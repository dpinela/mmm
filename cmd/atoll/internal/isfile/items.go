package isfile

import (
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlitex"
)

const (
	itemStatusConfirmed = 1
	itemStatusSaved     = 2
)

func (f *File) GetUnconfirmedItems(playerID PlayerID) (items []mwproto.DataReceiveMessage, err error) {
	return f.getItemsWithStatusBelow(playerID, itemStatusConfirmed)
}

func (f *File) GetUnsavedItems(playerID PlayerID) (items []mwproto.DataReceiveMessage, err error) {
	return f.getItemsWithStatusBelow(playerID, itemStatusSaved)
}

func (f *File) getItemsWithStatusBelow(playerID PlayerID, status int) (items []mwproto.DataReceiveMessage, err error) {
	err = sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp(`
		SELECT isi.label, isi.content, isi.source_player_id, ip.nickname
		FROM is_sent_items isi
			JOIN is_players ip ON isi.source_player_id = ip.player_id
			LEFT JOIN is_sent_item_statuses isis ON isi.item_id = isis.item_id AND isis.destination_player_id = ?
		WHERE COALESCE(isis.status, 0) < ?`)
		defer stmt.Close()
		stmt.BindInt64(1, int64(playerID))
		stmt.BindInt(2, status)
		return sqlitex.StepAll(stmt, func() {
			items = append(items, mwproto.DataReceiveMessage{
				Label:   stmt.ReadString(0),
				Content: stmt.ReadString(1),
				From:    stmt.ReadString(3),
				FromID:  int32(stmt.ReadInt32(2)),
			})
		})
	})
	return
}

func (f *File) SendItems(senderID PlayerID, items ...mwproto.Item) error {
	return sqlitex.WriteTransaction(f.db, func() error {
		stmt := f.db.PrepareTemp("INSERT INTO is_sent_items (label, content, source_player_id) VALUES (?, ?, ?)")
		defer stmt.Close()

		for _, item := range items {
			stmt.BindString(1, item.Label)
			stmt.BindString(2, item.Content)
			stmt.BindInt64(3, int64(senderID))
			if err := sqlitex.Exec(stmt); err != nil {
				return err
			}
		}

		return nil
	})
}

func (f *File) ConfirmItem(receiverID PlayerID, item mwproto.DataReceiveConfirmMessage) error {
	return sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp(`
		INSERT INTO is_sent_item_statuses (item_id, destination_player_id, status)
		SELECT isi.item_id, ?, ?
		FROM is_sent_items isi
			JOIN is_players ip ON isi.source_player_id = ip.player_id
		WHERE isi.label = ? AND isi.content = ? AND ip.nickname = ?
		ON CONFLICT DO NOTHING`)
		defer stmt.Close()
		stmt.BindInt64(1, int64(receiverID))
		stmt.BindInt(2, itemStatusConfirmed)
		stmt.BindString(3, item.Label)
		stmt.BindString(4, item.Data)
		stmt.BindString(5, item.From)
		return stmt.Exec()
	})
}

func (f *File) SaveConfirmedItems(playerID PlayerID) error {
	return sqlitex.RetryWhileBusy(func() error {
		stmt := f.db.PrepareTemp("UPDATE is_sent_item_statuses SET status = 2 WHERE destination_player_id = ? AND status = 1")
		defer stmt.Close()

		stmt.BindInt64(1, int64(playerID))
		return stmt.Exec()
	})
}
