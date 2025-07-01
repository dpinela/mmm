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
	go serveConsole(&cfg, db)
	return serve(&cfg, db)
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

func serve(cfg *serverConfig, db *database) error {
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
		go serveClient(conn, db)
	}
}

func serveClient(conn *mwproto.ServerConn, db *database) {
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
		log.Printf("unexpected message (awaiting connection) from %s: %+v", conn.RemoteAddr(), msg)
	}

	var roomInfo joinedRoom

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
			conn.Send(mwproto.JoinConfirmMessage{})
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %+v", conn.RemoteAddr(), msg)
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
		default:
			log.Printf("unexpected message (in room) from %s: %+v", conn.RemoteAddr(), msg)
		}
	}
}
