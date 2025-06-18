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
	return serve(cfg, db)
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

func serve(cfg serverConfig, db *database) error {
	listener, err := mwproto.Listen(cfg.ListenAddress)
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
			rid, ok, err := db.idOfRoom(msg.Room)
			if err != nil {
				log.Println(err)
				return
			}
			if !ok {
				log.Printf("%s tried to access nonexistent room %q", conn.RemoteAddr(), msg.Room)
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
				continue
			}
			log.Println(rid)
			conn.Send(mwproto.RequestRandoMessage{})
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %+v", conn.RemoteAddr(), msg)
		}
	}
}
