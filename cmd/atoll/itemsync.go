package main

import (
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strconv"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/isfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/ping"
	"github.com/dpinela/mmm/internal/mwproto"
)

type isyncNotifier struct {
	initiateTopic     ping.Topic[indexfile.ISRandoID]
	playerChangeTopic ping.Topic[indexfile.ISRandoID]
	itemTopic         ping.Topic[indexfile.ISRandoID]
}

func isyncPath(workdir string, randoID indexfile.ISRandoID) string {
	return filepath.Join(workdir, strconv.FormatInt(int64(randoID), 10)+".atollis")
}

func (srv *server) openIS(randoID indexfile.ISRandoID) (*isfile.File, error) {
	return isfile.Open(isyncPath(srv.workdir, randoID))
}

func (srv *server) serveItemSyncSetup(conn *mwproto.ServerConn, logger *slog.Logger, randoID indexfile.ISRandoID, readyMsg mwproto.ItemSyncReadyMessage) bool {
	logger = logger.With(logRandoID(randoID), logMode("is"))
	logger.Info("entered room setup")
	isync, err := srv.openIS(randoID)
	if err != nil {
		logger.Error(err.Error())
		return false
	}
	defer isync.Close()

	playerID, playerNames, err := isync.Join(readyMsg.Nickname, int(readyMsg.Hash), readyMsg.ReadyMetadata)
	if m, isBadHash := err.(isfile.HashMismatchError); isBadHash {
		conn.Send(mwproto.ReadyDenyMessage{Description: m.Error()})
		return true
	}
	if err != nil {
		logger.Error(err.Error())
		return false
	}

	logger = logger.With(logPlayerID(playerID))

	initiateCh, cancel := srv.isyncNotifier.initiateTopic.Listen(randoID)
	defer cancel()

	// so we don't get a notification looped back to us
	srv.isyncNotifier.playerChangeTopic.Notify(randoID)
	playersCh, cancel := srv.isyncNotifier.playerChangeTopic.Listen(randoID)
	defer cancel()

	conn.Send(readyConfirm(playerNames))

	settings, err := isync.GetGlobalSettings()
	if err != nil {
		logger.Error(err.Error())
		return false
	}
	if settings != nil {
		conn.Send(mwproto.InitiateSyncGameMessage{Settings: settings})
		playerNames, metadata, err := isync.GetFinalPlayers()
		if err != nil {
			log.Println(err)
			return false
		}
		conn.Send(mwproto.ResultMessage{
			PlayerID:              int32(playerID),
			RandoID:               int32(randoID),
			Nicknames:             playerNames,
			ReadyMetadata:         metadata,
			Placements:            map[string][]mwproto.ResultPlacement{},
			PlayerItemsPlacements: map[string]string{},
		})
	}

	weSentSettings := false

	var opLogger *slog.Logger

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return false
			}
			switch msg := msg.(type) {
			case mwproto.JoinMessage:
				if !(msg.Mode == mwproto.JoinModeIS && indexfile.ISRandoID(msg.RandoID) == randoID && isfile.PlayerID(msg.PlayerID) == playerID) {
					logger.Info(fmt.Sprintf("inconsistent Join: %+v", msg), logOp("Join"))
					continue
				}
				srv.serveItemSyncGame(conn, logger, isync, randoID, playerID)
				return false
			case mwproto.InitiateSyncGameMessage:
				if err := isync.SetGlobalSettings(msg.Settings); err != nil {
					logger.Error(err.Error(), logOp("InitiateSyncGame"))
					return false
				}
				weSentSettings = true
				srv.isyncNotifier.initiateTopic.Notify(randoID)
			case mwproto.UnreadyMessage:
				if err := isync.Unjoin(playerID); err != nil {
					logger.Error(err.Error(), logOp("Unready"))
					return false
				}
				return true
			case mwproto.RequestSettingsMessage, mwproto.ApplySettingsMessage:
				// expected, but useless; catch here so they don't pollute the log
			case mwproto.DisconnectMessage:
				return false
			default:
				logger.Info(fmt.Sprintf("unexpected message (in room setup): %#v", msg))
			}
		case <-initiateCh:
			if playerID == 0 {
				continue
			}
			opLogger = logger.With(logOp("_InitiateNotification"))
			settings, err := isync.GetGlobalSettings()
			if err != nil {
				opLogger.Error(err.Error())
				return false
			}
			if settings == nil {
				opLogger.Error("not having settings is impossible at this point")
				return false
			}
			if !weSentSettings {
				conn.Send(mwproto.InitiateSyncGameMessage{Settings: settings})
			}
			playerNames, metadata, err := isync.GetFinalPlayers()
			if err != nil {
				opLogger.Error(err.Error())
				return false
			}
			conn.Send(mwproto.ResultMessage{
				PlayerID:              int32(playerID),
				RandoID:               int32(randoID),
				Nicknames:             playerNames,
				ReadyMetadata:         metadata,
				Placements:            map[string][]mwproto.ResultPlacement{},
				PlayerItemsPlacements: map[string]string{},
			})
		case <-playersCh:
			playerNames, err := isync.PlayerNames()
			if err != nil {
				logger.Error(err.Error(), logOp("_PlayerChangeNotification"))
				return false
			}
			conn.Send(readyConfirm(playerNames))
		}
	}
}

func (srv *server) serveItemSyncGame(conn *mwproto.ServerConn, logger *slog.Logger, isync *isfile.File, randoID indexfile.ISRandoID, playerID isfile.PlayerID) {
	conn.Send(mwproto.JoinConfirmMessage{})

	itemNotifications, cancel := srv.isyncNotifier.itemTopic.Listen(randoID)
	defer cancel()

	unsavedItems, err := isync.GetUnsavedItems(playerID)
	if err != nil {
		logger.Error(err.Error(), logOp("_GetUnsavedItems"))
		return
	}

	logger.Info("joined")

	for _, item := range unsavedItems {
		conn.Send(item)
	}

	var opLogger *slog.Logger

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.DataSendMessage:
				opLogger = logger.With(logOp("DataSend"))
				if msg.To != mwproto.BroadcastPlayerID {
					opLogger.Info("invalid non-broadcast item", slog.String("label", msg.Label), slog.String("content", msg.Content), slog.Int("to", int(msg.To)))
					continue
				}
				item := mwproto.Item{
					Label:   msg.Label,
					Content: msg.Content,
					To:      msg.To,
				}
				if err := isync.SendItems(playerID, item); err != nil {
					opLogger.Error(err.Error())
					continue
				}
				conn.Send(mwproto.DataSendConfirmMessage{
					Label:   msg.Label,
					Content: msg.Content,
					To:      msg.To,
				})
				srv.isyncNotifier.itemTopic.Notify(randoID)
			case mwproto.DataReceiveConfirmMessage:
				if err := isync.ConfirmItem(playerID, msg); err != nil {
					logger.Error(err.Error(), logOp("DataReceiveConfirm"))
				}
			case mwproto.SaveMessage:
				if err := isync.SaveConfirmedItems(playerID); err != nil {
					logger.Error(err.Error(), logOp("Save"))
				}
			case mwproto.DisconnectMessage:
				return
			default:
				logger.Info(fmt.Sprintf("unexpected message (in game: %#v)", msg))
			}
		case <-itemNotifications:
			items, err := isync.GetUnconfirmedItems(playerID)
			if err != nil {
				logger.Error(err.Error(), logOp("_ItemNotification"))
				continue
			}
			for _, item := range items {
				conn.Send(item)
			}
		}
	}
}
