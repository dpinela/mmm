package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/dpinela/mmm/internal/mwproto"
)

type serverConfig struct {
	ListenAddress string
	MWPort        string
	ConsolePort   string
}

func main() {
	workdir := "."
	if len(os.Args) < 2 {
		os.Stderr.WriteString("no working directory specified, using .")
	} else {
		workdir = os.Args[1]
	}
	if err := run(workdir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(workdir string) error {
	cfg, err := loadConfig(filepath.Join(workdir, "config.json"))
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	db, err := openDB(filepath.Join(workdir, "storage.sqlite3"))
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	nf := newNotifier()
	go serveConsole(&cfg, db)
	return serve(&cfg, db, nf)
}

func loadConfig(filename string) (cfg serverConfig, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("load configuration: %w", err)
		}
	}()
	f, err := os.Open(filename)
	if err != nil {
		return
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&cfg)
	return
}

func serve(cfg *serverConfig, db *database, nf *notifier) error {
	listener, err := mwproto.Listen(cfg.ListenAddress + ":" + cfg.MWPort)
	if err != nil {
		return err
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go serveClient(conn, db, nf)
	}
}

func serveClient(conn *mwproto.ServerConn, db *database, nf *notifier) {
	defer conn.Close()

	for {
		msg, ok := <-conn.Inbox()
		if !ok {
			return
		}
		if _, ok := msg.(mwproto.ConnectMessage); ok {
			conn.Send(mwproto.ConnectMessage{ServerName: "Atoll"})
			break
		}
		log.Printf("unexpected message (awaiting connection) from %s: %#v", conn.RemoteAddr(), msg)
	}

	var roomInfo joinedRoom
	// More than one pending notification is redundant anyway. The sending side will drop
	// redundant ones automatically.
	itemNotifications := make(chan struct{}, 1)

waitingForReadyOrJoin:
	for {
		msg, ok := <-conn.Inbox()
		if !ok {
			return
		}
		switch msg := msg.(type) {
		case mwproto.DisconnectMessage:
			log.Printf("connection from %s terminated", conn.RemoteAddr())
			return
		case mwproto.ReadyMessage:
			if msg.Mode != 0 {
				log.Printf("invalid room mode from %s: %d", conn.RemoteAddr(), msg.Mode)
				conn.Send(mwproto.ReadyDenyMessage{Description: "invalid room mode"})
				continue
			}
			var err error
			roomInfo, err = db.joinRoom(msg.Room, msg.Nickname)
			if err == errRoomNotExist {
				log.Printf("%s tried to access nonexistent room %q", conn.RemoteAddr(), msg.Room)
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
				continue
			}
			if err != nil {
				log.Println(err)
				return
			}
			conn.Send(mwproto.ReadyConfirmMessage{Ready: 1, Names: []string{msg.Nickname}})
			conn.Send(mwproto.RequestRandoMessage{})
			break waitingForReadyOrJoin
		case mwproto.JoinMessage:
			log.Printf("%#v", msg)
			err := db.joinShuffledRoom(msg.RandoID, msg.PlayerID)
			switch err {
			case nil:
				roomInfo.randoID = int64(msg.RandoID)
				roomInfo.playerID = int64(msg.PlayerID)
				goto inRando
			case errRoomNotExist, errRoomNotShuffled:
				log.Printf("%s tried to access room %d, player %d: %v", conn.RemoteAddr(), msg.RandoID, msg.PlayerID, err)
				continue
			default:
				log.Println(err)
				return
			}
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %#v", conn.RemoteAddr(), msg)
		}
	}

	log.Printf("room ID = %d; player ID = %d", roomInfo.randoID, roomInfo.playerID)

	for {
		msg, ok := <-conn.Inbox()
		if !ok {
			return
		}
		switch msg := msg.(type) {
		case mwproto.RandoGeneratedMessage:
			switch roomInfo.status {
			case roomStatusOpen:
				if err := db.attachRando(roomInfo.randoID, roomInfo.playerID, msg); err != nil {
					log.Println(err)
					return
				}
			case roomStatusShuffling:
				log.Printf("room %d, player %d: tried to join with shuffle in progress", roomInfo.randoID, roomInfo.playerID)
			case roomStatusShuffled:
				result, err := db.getShuffleResult(roomInfo.randoID, roomInfo.playerID)
				if err != nil {
					log.Println(err)
					return
				}
				log.Println("hash:", result.GeneratedHash)
				conn.Send(result)
			}
		case mwproto.DisconnectMessage:
			log.Printf("connection from %s terminated", conn.RemoteAddr())
			return
		case mwproto.UnreadyMessage:
			goto waitingForReadyOrJoin
		case mwproto.JoinMessage:
			log.Printf("%#v", msg)
			err := db.joinShuffledRoom(msg.RandoID, msg.PlayerID)
			switch err {
			case nil:
				roomInfo.randoID = int64(msg.RandoID)
				roomInfo.playerID = int64(msg.PlayerID)
				goto inRando
				// get all pending items
			case errRoomNotExist, errRoomNotShuffled:
				log.Printf("%s tried to access room %d, player %d: %v", conn.RemoteAddr(), msg.RandoID, msg.PlayerID, err)
				continue
			default:
				log.Println(err)
				return
			}
		default:
			log.Printf("unexpected message (in room) from %s: %#v", conn.RemoteAddr(), msg)
		}
	}

inRando:
	conn.Send(mwproto.JoinConfirmMessage{})
	nf.listenNewItems(subscriberID{playerID: roomInfo.playerID, randoID: roomInfo.randoID}, itemNotifications)

	pendingItems, err := db.getUnsavedItems(roomInfo.randoID, roomInfo.playerID)
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("joined rando %d as player %d; sending %d unsaved items", roomInfo.randoID, roomInfo.playerID, len(pendingItems))

	for _, item := range pendingItems {
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
				if err := db.sendItem(roomInfo.randoID, roomInfo.playerID, int64(msg.To), msg.Label, msg.Content); err != nil {
					log.Println(err)
					return
				}
				conn.Send(mwproto.DataSendConfirmMessage{Label: msg.Label, Content: msg.Content, To: msg.To})
				nf.notifyNewItems(subscriberID{randoID: roomInfo.randoID, playerID: int64(msg.To)})
			case mwproto.DataReceiveConfirmMessage:
				if err := db.confirmItem(roomInfo.randoID, roomInfo.playerID, msg); err != nil {
					log.Println(err)
					return
				}
			case mwproto.SaveMessage:
				if err := db.markConfirmedItemsSaved(roomInfo.randoID, roomInfo.playerID); err != nil {
					log.Println(err)
					return
				}
			case mwproto.DisconnectMessage:
				log.Printf("connection from %s terminated", conn.RemoteAddr())
				return
			}
		case <-itemNotifications:
			items, err := db.getUnconfirmedItems(roomInfo.randoID, roomInfo.playerID)
			if err != nil {
				return
			}
			for _, item := range items {
				conn.Send(item)
			}
		}
	}
}
