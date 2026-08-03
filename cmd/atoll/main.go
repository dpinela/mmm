package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/isfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/mwfile"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlite"
)

type serverConfig struct {
	ListenAddress string
	MWPort        string
	ConsolePort   string
	Verbose       bool
}

type server struct {
	workdir       string
	mwNotifier    mwNotifier
	isyncNotifier isyncNotifier
	config        *serverConfig
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
	srv := &server{
		config:  &cfg,
		workdir: workdir,
	}
	go srv.serveConsole()
	return srv.serveClients()
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

func (srv *server) serveClients() error {
	listener, err := mwproto.Listen(srv.config.ListenAddress + ":" + srv.config.MWPort)
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
		go srv.serveClient(conn)
	}
}

func (srv *server) openIndex() (*indexfile.File, error) {
	return indexfile.Open(filepath.Join(srv.workdir, "index.atolldb"))
}

func (srv *server) serveClient(conn *mwproto.ServerConn) {
	defer conn.Close()

	index, err := srv.openIndex()
	if err != nil {
		log.Println("open index database:", err)
		return
	}
	defer index.Close()

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

	var (
		mw       *mwfile.File
		mwID     indexfile.MWRandoID
		isID     indexfile.ISRandoID
		playerID mwfile.PlayerID
	)

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
			mwID, err = index.FindRoom(msg.Room)
			if err == indexfile.ErrRoomNotExist {
				log.Printf("%q tried to access nonexistent room %q", msg.Nickname, msg.Room)
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
				continue
			}
			if err != nil {
				log.Println(err)
				return
			}

			mw, err = srv.openMW(mwID)
			if errors.Is(err, sqlite.ErrCantOpen) {
				// deleting the file effectively deletes the room
				if err := index.DeleteRoom(mwID); err != nil {
					log.Println(err)
				}
				log.Printf("%q tried to access nonexistent room %q", msg.Nickname, msg.Room)
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
				return
			}
			if err != nil {
				log.Println(err)
				return
			}
			defer mw.Close()
			keepAlive := srv.serveMultiworldSetup(conn, mw, mwID, msg)
			if keepAlive {
				continue
			}
			return
		case mwproto.ItemSyncReadyMessage:
			isID, err = index.FindISRoom(msg.Room)
			if err != nil {
				log.Println(err)
				return
			}
			keepAlive := srv.serveItemSyncSetup(conn, isID, msg)
			if keepAlive {
				continue
			}
			return
		case mwproto.JoinMessage:
			switch msg.Mode {
			case mwproto.JoinModeMW:
				mwID = indexfile.MWRandoID(msg.RandoID)
				playerID = mwfile.PlayerID(msg.PlayerID)
				mw, err = srv.openMW(mwID)
				if err != nil {
					log.Println(err)
					return
				}
				defer mw.Close()
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					log.Println(err)
					return
				}
				if isShuffled {
					srv.serveMultiworldGame(conn, mw, mwID, playerID)
					return
				} else {
					log.Printf("rando %d isn't shuffled yet", mwID)
				}
			case mwproto.JoinModeIS:
				isID = indexfile.ISRandoID(msg.RandoID)
				isyncPlayerID := isfile.PlayerID(msg.PlayerID)
				isync, err := srv.openIS(isID)
				if err != nil {
					log.Println(err)
					return
				}
				defer isync.Close()
				srv.serveItemSyncGame(conn, isync, isID, isyncPlayerID)
				return
			default:
				log.Printf("player %d (%q) tried to join rando %d in unknown mode %d", msg.PlayerID, msg.DisplayName, msg.RandoID, msg.Mode)
			}
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %#v", conn.RemoteAddr(), msg)
		}
	}
}

func readyConfirm(names []string) mwproto.ReadyConfirmMessage {
	return mwproto.ReadyConfirmMessage{
		Ready: int32(len(names)),
		Names: names,
	}
}
