package main

import (
	"log"
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

func (srv *server) serveMultiworldSetup(conn *mwproto.ServerConn, mw *mwfile.File, mwID indexfile.MWRandoID, msg mwproto.ReadyMessage) bool {
	playerID, playerNames, err := mw.Join(msg.Nickname)
	if err != nil {
		log.Println(err)
		return false
	}
	srv.mwNotifier.playerChangeTopic.Notify(mwID)
	conn.Send(readyConfirm(playerNames))
	conn.Send(mwproto.RequestRandoMessage{})

	log.Printf("room ID = %d; player ID = %d", mwID, playerID)

	// There is a tiny window from Join to here where a shuffle notification from
	// another goroutine would be lost, but the room wasn't shuffled yet when we checked
	// so we're stuck waiting.
	// In the very unlikely event this happens, the client can disconnect and reconnect
	// to resolve the issue.
	shuffleNotifications, cancel := srv.mwNotifier.shuffleTopic.Listen(mwID)
	defer cancel()
	playerChangeNotifications, cancel := srv.mwNotifier.playerChangeTopic.Listen(mwID)
	defer cancel()

	var attachedRando *mwproto.RandoGeneratedMessage

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return false
			}

			switch msg := msg.(type) {
			case mwproto.RandoGeneratedMessage:
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					log.Println(err)
					return false
				}
				if isShuffled {
					err = sendRandoResult(conn, mw, mwID, playerID, msg)
				} else {
					err = mw.Attach(playerID, msg)
				}
				if err != nil {
					log.Println(err)
					return false
				}
			case mwproto.DisconnectMessage:
				log.Printf("connection from %s terminated", conn.RemoteAddr())
				return false
			case mwproto.UnreadyMessage:
				return true
			case mwproto.JoinMessage:
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					log.Println(err)
					return false
				}
				if !isShuffled {
					log.Printf("%s tried to access room %d, player %d: %v", conn.RemoteAddr(), msg.RandoID, msg.PlayerID, err)
					continue
				}
				srv.serveMultiworldGame(conn, mw, mwID, playerID)
				return false
			default:
				log.Printf("unexpected message (in room) from %s: %#v", conn.RemoteAddr(), msg)
			}
		case <-shuffleNotifications:
			if attachedRando == nil {
				continue
			}
			if err := sendRandoResult(conn, mw, mwID, playerID, *attachedRando); err != nil {
				log.Println(err)
				return false
			}
		case <-playerChangeNotifications:
			names, err := mw.PlayerNames()
			if err != nil {
				log.Println(err)
				return false
			}
			conn.Send(readyConfirm(names))
		}
	}
}

func (srv *server) serveMultiworldGame(conn *mwproto.ServerConn, mw *mwfile.File, randoID indexfile.MWRandoID, playerID mwfile.PlayerID) {
	conn.Send(mwproto.JoinConfirmMessage{})

	sid := subscriberID{playerID: int64(playerID), randoID: randoID}
	itemNotifications, cancel := srv.mwNotifier.itemTopic.Listen(sid)
	defer cancel()
	notchCostNotifications, cancel := srv.mwNotifier.notchCostTopic.Listen(randoID)
	defer cancel()

	pendingItems, err := mw.GetUnsavedItems(playerID)
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("joined rando %d as player %d; sending %d unsaved items", randoID, playerID, len(pendingItems))

	for _, item := range pendingItems {
		conn.Send(item)
	}

	gotNotchCosts, err := mw.HasNotchCosts(playerID)
	if err != nil {
		log.Println(err)
		return
	}
	if !gotNotchCosts {
		conn.Send(mwproto.RequestCharmNotchCostsMessage{})
	}

	othersCosts, err := mw.GetUnconfirmedNotchCosts(playerID)
	if err != nil {
		log.Println(err)
		return
	}
	for playerID, costs := range othersCosts {
		log.Printf("rando %d, player %d: sending notch costs of player %d", randoID, playerID, playerID)
		conn.Send(mwproto.AnnounceCharmNotchCostsMessage{PlayerID: int32(playerID), NotchCosts: costs})
	}

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.AnnounceCharmNotchCostsMessage:
				if gotNotchCosts {
					log.Printf("received duplicate notch costs for rando %d, player %d; ignoring", randoID, playerID)
					continue
				}
				if int64(msg.PlayerID) != int64(playerID) {
					log.Printf("rando %d, player %d sent notch costs for a different player (%d); ignoring", randoID, playerID, msg.PlayerID)
					continue
				}
				if err := mw.SaveNotchCosts(playerID, msg.NotchCosts); err != nil {
					log.Println(err)
					continue
				}
				log.Printf("received notch costs for rando %d, player %d", randoID, playerID)
				// This notification will echo back to this goroutine as well, but that's harmless.
				// (except it may cause a notch cost announcement to get duplicated)
				srv.mwNotifier.notchCostTopic.Notify(randoID)
			case mwproto.ConfirmCharmNotchCostsReceivedMessage:
				log.Printf("rando %d, player %d confirmed notch costs of player %d", randoID, playerID, msg.PlayerID)
				if err := mw.ConfirmNotchCosts(mwfile.PlayerID(msg.PlayerID), playerID); err != nil {
					log.Println(err)
					continue
				}
			case mwproto.DataSendMessage:
				if err := mw.SendItems(playerID, mwproto.Item{To: msg.To, Label: msg.Label, Content: msg.Content}); err != nil {
					log.Println(err)
					return
				}
				conn.Send(mwproto.DataSendConfirmMessage{Label: msg.Label, Content: msg.Content, To: msg.To})
				srv.mwNotifier.itemTopic.Notify(subscriberID{randoID: randoID, playerID: int64(msg.To)})
			case mwproto.DatasSendMessage:
				if err := mw.SendItems(playerID, msg.Datas...); err != nil {
					log.Println(err)
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
					log.Println(err)
					return
				}
			case mwproto.SaveMessage:
				if err := mw.SaveConfirmedItems(playerID); err != nil {
					log.Println(err)
					return
				}
			case mwproto.DisconnectMessage:
				log.Printf("connection from %s terminated", conn.RemoteAddr())
				return
			}
		case <-itemNotifications:
			items, err := mw.GetUnconfirmedItems(playerID)
			if err != nil {
				log.Println(err)
				return
			}
			for _, item := range items {
				conn.Send(item)
			}
		case <-notchCostNotifications:
			othersCosts, err := mw.GetUnconfirmedNotchCosts(playerID)
			if err != nil {
				log.Println(err)
				return
			}
			for playerID, costs := range othersCosts {
				log.Printf("rando %d, player %d: sending notch costs of player %d", randoID, playerID, playerID)
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
