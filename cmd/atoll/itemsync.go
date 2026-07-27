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

	conn.Send(readyConfirm(playerNames))

	settings, err := isync.GetGlobalSettings()
	if err != nil {
		log.Println(err)
		return
	}
	if settings != nil {
		conn.Send(mwproto.InitiateSyncGameMessage{Settings: settings})
	}

	weSentSettings := false

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}
			switch msg := msg.(type) {
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
				conn.Send(mwproto.ApplySettingsMessage{Settings: settings})
			}
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
