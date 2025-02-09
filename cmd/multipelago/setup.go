package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/dpinela/mmm/internal/mwproto"
)

func setupMW(opts options, data apdata) error {
	if len(data.SlotInfo) != 1 {
		return fmt.Errorf(".archipelago contains %d slots, expected only one", len(data.SlotInfo))
	}
	slotID := singularKey(data.SlotInfo)
	slot := data.SlotInfo[slotID]
	nickname := slot.Name

	mwPlacements, err := apToMWPlacements(data)
	if err != nil {
		return fmt.Errorf("convert AP to MW: %w", err)
	}

	conn, err := mwproto.Dial(opts.mwserver)
	if err != nil {
		return fmt.Errorf("connect to MW: %w", err)
	}
	defer conn.Close()
	inbox := conn.Inbox()

	conn.Send(mwproto.ConnectMessage{})

	for {
		msg, ok := <-inbox
		if !ok {
			return errConnectionLost
		}
		if c, ok := msg.(mwproto.ConnectMessage); ok {
			log.Println("connected to", c.ServerName)
			break
		}
		log.Printf("unexpected message before connect: %#v", msg)
	}

	conn.Send(mwproto.ReadyMessage{
		Room:          opts.mwroom,
		Nickname:      nickname,
		ReadyMetadata: []mwproto.KeyValuePair{},
	})

waitingToEnterRoom:
	for {
		msg, ok := <-inbox
		if !ok {
			return errConnectionLost
		}
		switch msg := msg.(type) {
		case mwproto.ReadyConfirmMessage:
			log.Printf("joined room %s with players %v", opts.mwroom, msg.Names)
			break waitingToEnterRoom
		case mwproto.ReadyDenyMessage:
			log.Printf("denied entry to room %s: %s", opts.mwroom, msg.Description)
		case mwproto.DisconnectMessage:
			return errConnectionLost
		case mwproto.RequestRandoMessage:
		default:
			log.Printf("unexpected message while joining room: %#v", msg)
		}
	}

waitingForStartMW:
	for {
		msg, ok := <-inbox
		if !ok {
			return errConnectionLost
		}
		switch msg := msg.(type) {
		case mwproto.DisconnectMessage:
			return errConnectionLost
		case mwproto.ReadyConfirmMessage:
			log.Printf("players in room: %v", msg.Names)
		case mwproto.RequestRandoMessage:
			conn.Send(mwproto.RandoGeneratedMessage{
				Items: map[string][]mwproto.Placement{
					singularItemGroup: mwPlacements,
				},
				Seed: 666_666_666,
			})
			break waitingForStartMW
		default:
			log.Printf("unexpected message while in room: %#v", msg)
		}
	}

	var mwResult mwproto.ResultMessage

waitingForResult:
	for {
		msg, ok := <-inbox
		if !ok {
			return errConnectionLost
		}
		switch msg := msg.(type) {
		case mwproto.DisconnectMessage:
			return errConnectionLost
		case mwproto.ResultMessage:
			mwResult = msg
			break waitingForResult
		}
	}

	if err := os.Mkdir(opts.workdir, 0700); err != nil {
		return err
	}

	mwResultFile, err := os.Create(filepath.Join(opts.workdir, mwResultFileName))
	if err != nil {
		return err
	}
	defer mwResultFile.Close()
	enc := json.NewEncoder(mwResultFile)
	enc.SetIndent("", "  ")
	return enc.Encode(mwResult)
}