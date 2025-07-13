package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"

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
	go serveConsole(&cfg, db, nf)
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
	shuffleNotifications := make(chan struct{}, 1)

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
				serveClientInGame(conn, db, nf, joinedRoom{randoID: int64(msg.RandoID), playerID: int64(msg.PlayerID)})
				return
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

	// There is a tiny window from db.joinRoom to here where a shuffle notification from
	// another goroutine would be lost, but the room wasn't shuffled yet when we checked
	// so we're stuck waiting.
	// In the very unlikely event this happens, the client can disconnect and reconnect
	// to resolve the issue.
	nf.listenShuffleDone(roomInfo.randoID, shuffleNotifications)
	defer nf.muteShuffleDone(roomInfo.randoID, shuffleNotifications)

	var attachedRando *mwproto.RandoGeneratedMessage

	for {
		select {
		case msg, ok := <-conn.Inbox():
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
					attachedRando = &msg
				case roomStatusShuffling:
					log.Printf("room %d, player %d: tried to join with shuffle in progress", roomInfo.randoID, roomInfo.playerID)
				case roomStatusShuffled:
					if err := sendRandoResult(conn, db, roomInfo, msg); err != nil {
						log.Println(err)
						return
					}
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
					serveClientInGame(conn, db, nf, roomInfo)
					return
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
		case <-shuffleNotifications:
			roomInfo.status = roomStatusShuffled
			if attachedRando == nil {
				continue
			}
			if err := sendRandoResult(conn, db, roomInfo, *attachedRando); err != nil {
				log.Println(err)
				return
			}
		}
	}
}

func sendRandoResult(conn *mwproto.ServerConn, db *database, roomInfo joinedRoom, currentRando mwproto.RandoGeneratedMessage) error {
	result, err := db.getShuffleResult(roomInfo.randoID, roomInfo.playerID)
	if err != nil {
		return err
	}
	origSeed, err := db.getAttachedRando(roomInfo.randoID, roomInfo.playerID)
	if err != nil {
		return err
	}
	log.Println("hash:", result.GeneratedHash)
	if !reflect.DeepEqual(origSeed, currentRando) {
		result.GeneratedHash = "ERROR: seed does not match original"
	}
	conn.Send(result)
	return nil
}

func serveClientInGame(conn *mwproto.ServerConn, db *database, nf *notifier, roomInfo joinedRoom) {
	conn.Send(mwproto.JoinConfirmMessage{})
	itemNotifications := make(chan struct{}, 1)
	sid := subscriberID{playerID: roomInfo.playerID, randoID: roomInfo.randoID}
	nf.listenNewItems(sid, itemNotifications)
	defer nf.muteNewItems(sid, itemNotifications)

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
