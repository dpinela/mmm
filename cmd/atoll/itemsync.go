package main

import (
	"log"
	"path/filepath"
	"strconv"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/isfile"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/ping"
)

type isyncNotifier struct {
	initiateTopic     ping.Topic[indexfile.ISRandoID]
	playerChangeTopic ping.Topic[indexfile.ISRandoID]
	itemTopic         ping.Topic[indexfile.ISRandoID]
}

func isyncPath(workdir string, randoID indexfile.ISRandoID) string {
	return filepath.Join(workdir, strconv.FormatInt(int64(randoID), 10)+".atollis")
}

func openIS(workdir string, randoID indexfile.ISRandoID) (*isfile.File, error) {
	return isfile.Open(isyncPath(workdir, randoID))
}

func serveItemSyncSetup(conn *mwproto.ServerConn, workdir string, nf *isyncNotifier, randoID indexfile.ISRandoID, readyMsg mwproto.ItemSyncReadyMessage) {
	isync, err := openIS(workdir, randoID)
	if err != nil {
		log.Println(err)
		return
	}
	defer isync.Close()

	playerID, playerNames, err := isync.Join(readyMsg.Nickname, int(readyMsg.Hash), readyMsg.ReadyMetadata)
	if m, isBadHash := err.(isfile.HashMismatchError); isBadHash {
		conn.Send(mwproto.ReadyDenyMessage{Description: m.Error()})
		return
	}
	if err != nil {
		log.Println(err)
		return
	}

	initiateCh := make(chan struct{}, 1)
	playersCh := make(chan struct{}, 1)

	nf.initiateTopic.Listen(randoID, initiateCh)
	defer nf.initiateTopic.Mute(randoID, initiateCh)

	// so we don't get a notification looped back to us
	nf.playerChangeTopic.Notify(randoID)
	nf.playerChangeTopic.Listen(randoID, playersCh)
	defer nf.playerChangeTopic.Mute(randoID, playersCh)

	log.Println("player names:", playerNames)

	conn.Send(readyConfirm(playerNames))

	settings, err := isync.GetGlobalSettings()
	if err != nil {
		log.Println(err)
		return
	}
	if settings != nil {
		log.Println("existing settings found")
		conn.Send(mwproto.InitiateSyncGameMessage{Settings: settings})
		playerNames, metadata, err := isync.GetFinalPlayers()
		if err != nil {
			log.Println(err)
			return
		}
		log.Println("new player names:", playerNames)
		log.Println("metadata:", metadata)
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

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.JoinMessage:
				if !(msg.Mode == mwproto.JoinModeIS && indexfile.ISRandoID(msg.RandoID) == randoID && isfile.PlayerID(msg.PlayerID) == playerID) {
					log.Printf("inconsistent Join: %+v", msg)
					continue
				}
				serveISClientInGame(conn, isync, nf, randoID, playerID)
				return
			case mwproto.InitiateSyncGameMessage:
				if err := isync.SetGlobalSettings(msg.Settings); err != nil {
					log.Println(err)
					return
				}
				weSentSettings = true
				nf.initiateTopic.Notify(randoID)
			case mwproto.UnreadyMessage:
				if err := isync.Unjoin(playerID); err != nil {
					log.Println(err)
					return
				}
			case mwproto.RequestSettingsMessage, mwproto.ApplySettingsMessage:
				// expected, but useless; catch here so they don't pollute the log
			case mwproto.DisconnectMessage:
				return
			default:
				log.Printf("unexpected message (in IS room setup): %#v", msg)
			}
		case <-initiateCh:
			if playerID == 0 {
				continue
			}
			settings, err := isync.GetGlobalSettings()
			if err != nil {
				log.Println(err)
				return
			}
			if settings == nil {
				log.Println("not having settings is impossible at this point")
				continue
			}
			if !weSentSettings {
				conn.Send(mwproto.InitiateSyncGameMessage{Settings: settings})
			}
			playerNames, metadata, err := isync.GetFinalPlayers()
			if err != nil {
				log.Println(err)
				return
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
				log.Println(err)
				return
			}
			conn.Send(readyConfirm(playerNames))
		}
	}
}

func serveISClientInGame(conn *mwproto.ServerConn, isync *isfile.File, nf *isyncNotifier, randoID indexfile.ISRandoID, playerID isfile.PlayerID) {
	conn.Send(mwproto.JoinConfirmMessage{})

	itemNotifications := make(chan struct{}, 1)
	nf.itemTopic.Listen(randoID, itemNotifications)
	defer nf.itemTopic.Mute(randoID, itemNotifications)

	unsavedItems, err := isync.GetUnsavedItems(playerID)
	if err != nil {
		log.Println(err)
		return
	}

	for _, item := range unsavedItems {
		conn.Send(item)
	}

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.DataSendMessage:
				if msg.To != mwproto.BroadcastPlayerID {
					log.Printf("attempted to send item (%q, %q) to player %d; only broadcasts are allowed", msg.Label, msg.Content, msg.To)
					continue
				}
				item := mwproto.Item{
					Label:   msg.Label,
					Content: msg.Content,
					To:      msg.To,
				}
				if err := isync.SendItems(playerID, item); err != nil {
					log.Println(err)
					continue
				}
				conn.Send(mwproto.DataSendConfirmMessage{
					Label:   msg.Label,
					Content: msg.Content,
					To:      msg.To,
				})
				nf.itemTopic.Notify(randoID)
			case mwproto.DataReceiveConfirmMessage:
				if err := isync.ConfirmItem(playerID, msg); err != nil {
					log.Println(err)
				}
			case mwproto.SaveMessage:
				if err := isync.SaveConfirmedItems(playerID); err != nil {
					log.Println(err)
				}
			case mwproto.DisconnectMessage:
				return
			default:
				log.Printf("unexpected message (in IS gameplay): %#v", msg)
			}
		case <-itemNotifications:
			items, err := isync.GetUnconfirmedItems(playerID)
			if err != nil {
				log.Println(err)
				continue
			}
			for _, item := range items {
				conn.Send(item)
			}
		}
	}
}
