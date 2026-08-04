package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/mwfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/ping"
	"github.com/dpinela/mmm/internal/mwproto"
)

type mwNotifier struct {
	notchCostTopic    ping.Topic[indexfile.MWRandoID]
	shuffleTopic      ping.Topic[indexfile.MWRandoID]
	playerChangeTopic ping.Topic[indexfile.MWRandoID]
	itemTopic         ping.Topic[subscriberID]
}

type subscriberID struct {
	randoID  indexfile.MWRandoID
	playerID int64
}

func mwPath(workdir string, randoID indexfile.MWRandoID) string {
	return filepath.Join(workdir, strconv.FormatInt(int64(randoID), 10)+".atollmw")
}

func (srv *server) openMW(randoID indexfile.MWRandoID) (*mwfile.File, error) {
	return mwfile.Open(mwPath(srv.workdir, randoID))
}

func (srv *server) serveMultiworldSetup(conn *mwproto.ServerConn, logger *slog.Logger, mw *mwfile.File, mwID indexfile.MWRandoID, msg mwproto.ReadyMessage) bool {
	logger = logger.With(logMode("mw"), logRandoID(mwID))
	logger.Info("entered room setup")

	playerID, playerNames, err := mw.Join(msg.Nickname)
	if err != nil {
		logger.Error(err.Error(), logOp("Join"))
		return false
	}

	logger = logger.With(logPlayerID(playerID))

	srv.mwNotifier.playerChangeTopic.Notify(mwID)
	conn.Send(readyConfirm(playerNames))
	conn.Send(mwproto.RequestRandoMessage{})

	// There is a tiny window from Join to here where a shuffle notification from
	// another goroutine would be lost, but the room wasn't shuffled yet when we checked
	// so we're stuck waiting.
	// In the very unlikely event this happens, the client can disconnect and reconnect
	// to resolve the issue.
	shuffleNotifications, cancel := srv.mwNotifier.shuffleTopic.Listen(mwID)
	defer cancel()
	playerChangeNotifications, cancel := srv.mwNotifier.playerChangeTopic.Listen(mwID)
	defer cancel()

	var (
		attachedRando *mwproto.RandoGeneratedMessage
		opLogger      *slog.Logger
	)

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return false
			}

			switch msg := msg.(type) {
			case mwproto.RandoGeneratedMessage:
				opLogger = logger.With(logOp("RandoGenerated"))
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					opLogger.Error(err.Error())
					return false
				}
				if isShuffled {
					err = sendRandoResult(conn, mw, mwID, playerID, msg)
				} else {
					err = mw.Attach(playerID, msg)
				}
				if err != nil {
					opLogger.Error(err.Error())
					return false
				}
				attachedRando = &msg
			case mwproto.DisconnectMessage:
				return false
			case mwproto.UnreadyMessage:
				logger.Info("exited room setup")
				return true
			case mwproto.JoinMessage:
				opLogger = logger.With(logOp("Join"))
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					opLogger.Error(err.Error())
					return false
				}
				if !(msg.Mode == mwproto.JoinModeIS && indexfile.MWRandoID(msg.RandoID) == mwID && mwfile.PlayerID(msg.PlayerID) == playerID) {
					opLogger.Info(fmt.Sprintf("inconsistent Join: %+v", msg), logOp("Join"))
					continue
				}
				if !isShuffled {
					opLogger.Info("room is not shuffled")
					continue
				}
				srv.serveMultiworldGame(conn, logger, mw, mwID, playerID)
				return false
			default:
				logger.Info(fmt.Sprintf("unexpected message (in room setup): %#v", msg))
			}
		case <-shuffleNotifications:
			if attachedRando == nil {
				continue
			}
			if err := sendRandoResult(conn, mw, mwID, playerID, *attachedRando); err != nil {
				logger.Error(err.Error(), logOp("_ShuffleNotification"))
				return false
			}
		case <-playerChangeNotifications:
			names, err := mw.PlayerNames()
			if err != nil {
				logger.Error(err.Error(), logOp("_PlayerChangeNotification"))
				return false
			}
			conn.Send(readyConfirm(names))
		}
	}
}

func (srv *server) serveMultiworldGame(conn *mwproto.ServerConn, logger *slog.Logger, mw *mwfile.File, randoID indexfile.MWRandoID, playerID mwfile.PlayerID) {
	conn.Send(mwproto.JoinConfirmMessage{})
	logger.Info("joined")

	sid := subscriberID{playerID: int64(playerID), randoID: randoID}
	itemNotifications, cancel := srv.mwNotifier.itemTopic.Listen(sid)
	defer cancel()
	notchCostNotifications, cancel := srv.mwNotifier.notchCostTopic.Listen(randoID)
	defer cancel()

	pendingItems, err := mw.GetUnsavedItems(playerID)
	if err != nil {
		logger.Error(err.Error(), logOp("_GetUnsavedItems"))
		return
	}

	logger.Info(fmt.Sprintf("sending %d unsaved items", len(pendingItems)))

	for _, item := range pendingItems {
		conn.Send(item)
	}

	gotNotchCosts, err := mw.HasNotchCosts(playerID)
	if err != nil {
		logger.Error(err.Error(), logOp("_HasNotchCosts"))
		return
	}
	if !gotNotchCosts {
		conn.Send(mwproto.RequestCharmNotchCostsMessage{})
	}

	othersCosts, err := mw.GetUnconfirmedNotchCosts(playerID)
	if err != nil {
		logger.Error(err.Error(), logOp("_GetUnconfirmedNotchCosts"))
		return
	}
	for playerID, costs := range othersCosts {
		conn.Send(mwproto.AnnounceCharmNotchCostsMessage{PlayerID: int32(playerID), NotchCosts: costs})
	}

	var opLogger *slog.Logger

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.AnnounceCharmNotchCostsMessage:
				opLogger = logger.With(logOp("AnnounceCharmNotchCosts"))
				if gotNotchCosts {
					opLogger.Info("received duplicate notch costs")
					continue
				}
				if int64(msg.PlayerID) != int64(playerID) {
					opLogger.Info("sent notch costs for wrong player; ignoring", slog.Int64("otherPlayerID", int64(msg.PlayerID)))
					continue
				}
				if err := mw.SaveNotchCosts(playerID, msg.NotchCosts); err != nil {
					opLogger.Error(err.Error())
					continue
				}
				// This notification will echo back to this goroutine as well, but that's harmless.
				// (except it may cause a notch cost announcement to get duplicated)
				srv.mwNotifier.notchCostTopic.Notify(randoID)
			case mwproto.ConfirmCharmNotchCostsReceivedMessage:
				if err := mw.ConfirmNotchCosts(mwfile.PlayerID(msg.PlayerID), playerID); err != nil {
					logger.Error(err.Error(), logOp("ConfirmCharmNotchCostsReceived"))
					continue
				}
			case mwproto.DataSendMessage:
				if err := mw.SendItems(playerID, mwproto.Item{To: msg.To, Label: msg.Label, Content: msg.Content}); err != nil {
					logger.Error(err.Error(), logOp("DataSend"))
					return
				}
				conn.Send(mwproto.DataSendConfirmMessage{Label: msg.Label, Content: msg.Content, To: msg.To})
				srv.mwNotifier.itemTopic.Notify(subscriberID{randoID: randoID, playerID: int64(msg.To)})
			case mwproto.DatasSendMessage:
				if err := mw.SendItems(playerID, msg.Datas...); err != nil {
					logger.Error(err.Error(), logOp("DatasSend"))
					return
				}
				conn.Send(mwproto.DatasSendConfirmMessage{DatasCount: int32(len(msg.Datas))})
				destinationPlayers := map[int32]struct{}{}
				for _, item := range msg.Datas {
					destinationPlayers[item.To] = struct{}{}
				}
				for p := range destinationPlayers {
					srv.mwNotifier.itemTopic.Notify(subscriberID{randoID: randoID, playerID: int64(p)})
				}
			case mwproto.DataReceiveConfirmMessage:
				if err := mw.ConfirmItem(playerID, msg); err != nil {
					logger.Error(err.Error(), logOp("DataReceiveConfirm"))
					return
				}
			case mwproto.SaveMessage:
				if err := mw.SaveConfirmedItems(playerID); err != nil {
					logger.Error(err.Error(), logOp("Save"))
					return
				}
			case mwproto.DisconnectMessage:
				return
			}
		case <-itemNotifications:
			items, err := mw.GetUnconfirmedItems(playerID)
			if err != nil {
				logger.Error(err.Error(), "_ItemNotificaton")
				return
			}
			for _, item := range items {
				conn.Send(item)
			}
		case <-notchCostNotifications:
			othersCosts, err := mw.GetUnconfirmedNotchCosts(playerID)
			if err != nil {
				logger.Error(err.Error(), "_NotchCostsNotification")
				return
			}
			for playerID, costs := range othersCosts {
				conn.Send(mwproto.AnnounceCharmNotchCostsMessage{PlayerID: int32(playerID), NotchCosts: costs})
			}
		}
	}
}

func sendRandoResult(conn *mwproto.ServerConn, mw *mwfile.File, randoID indexfile.MWRandoID, playerID mwfile.PlayerID, currentRando mwproto.RandoGeneratedMessage) error {
	result, err := mw.GetShuffleResult(playerID)
	if err != nil {
		return err
	}
	result.RandoID = int32(randoID)
	origSeed, err := mw.GetWorld(playerID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(origSeed, currentRando) {
		result.GeneratedHash = "ERROR: seed does not match original"
	}

	conn.Send(result)
	return nil
}
