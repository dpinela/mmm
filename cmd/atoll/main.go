package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
			slog.Error(err.Error())
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

	logger := slog.Default().With(slog.String("conn", fmt.Sprintf("%p", conn)))

	logger.Info("accepted connection")

	index, err := srv.openIndex()
	if err != nil {
		logger.Error(err.Error(), slog.String("op", "open index database"))
		return
	}
	defer index.Close()

	logger.Info("waiting for Connect")

	for {
		msg, ok := <-conn.Inbox()
		if !ok {
			return
		}
		if _, ok := msg.(mwproto.ConnectMessage); ok {
			conn.Send(mwproto.ConnectMessage{ServerName: "Atoll"})
			break
		}
		logger.Info(fmt.Sprintf("unexpected message while waiting for connect: %#v", msg))
	}

	var (
		mw       *mwfile.File
		mwID     indexfile.MWRandoID
		isID     indexfile.ISRandoID
		playerID mwfile.PlayerID
		opLogger *slog.Logger
	)

	for {
		msg, ok := <-conn.Inbox()
		if !ok {
			return
		}
		switch msg := msg.(type) {
		case mwproto.DisconnectMessage:
			logger.Info("client requested disconnection")
			return
		case mwproto.ReadyMessage:
			opLogger = logger.With(logOp("Ready"))
			if msg.Mode != 0 {
				opLogger.Info("invalid mode", slog.Int("modenum", int(msg.Mode)))
				conn.Send(mwproto.ReadyDenyMessage{Description: "invalid room mode"})
				continue
			}

			opLogger = opLogger.With(logMode("mw"))

			roomNotExist := func() {
				opLogger.Info("room does not exist", slog.String("room", msg.Room), slog.String("nickname", msg.Nickname))
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
			}

			mwID, err = index.FindRoom(msg.Room)
			if err == indexfile.ErrRoomNotExist {
				roomNotExist()
				continue
			}
			if err != nil {
				opLogger.Error(err.Error())
				return
			}

			mw, err = srv.openMW(mwID)
			if errors.Is(err, sqlite.ErrCantOpen) {
				// deleting the file effectively deletes the room
				if err := index.DeleteRoom(mwID); err != nil {
					opLogger.Warn(err.Error())
				}
				roomNotExist()
				return
			}
			if err != nil {
				opLogger.Error(err.Error())
				return
			}
			defer mw.Close()
			keepAlive := srv.serveMultiworldSetup(conn, logger, mw, mwID, msg)
			if keepAlive {
				continue
			}
			return
		case mwproto.ItemSyncReadyMessage:
			isID, err = index.FindISRoom(msg.Room)
			if err != nil {
				logger.Error(err.Error(), logOp("ItemSyncReady"))
				return
			}
			keepAlive := srv.serveItemSyncSetup(conn, logger, isID, msg)
			if keepAlive {
				continue
			}
			return
		case mwproto.JoinMessage:
			opLogger = logger.With(logOp("Join"))
			switch msg.Mode {
			case mwproto.JoinModeMW:
				opLogger = opLogger.With(logMode("mw"))
				mwID = indexfile.MWRandoID(msg.RandoID)
				playerID = mwfile.PlayerID(msg.PlayerID)
				mw, err = srv.openMW(mwID)
				if err != nil {
					opLogger.Error(err.Error())
					return
				}
				defer mw.Close()
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					opLogger.Error(err.Error())
					return
				}
				if isShuffled {
					index.Close()
					srv.serveMultiworldGame(conn, logger, mw, mwID, playerID)
					return
				} else {
					opLogger.Info("rando not shuffled yet", logRandoID(mwID))
				}
			case mwproto.JoinModeIS:
				isID = indexfile.ISRandoID(msg.RandoID)
				isyncPlayerID := isfile.PlayerID(msg.PlayerID)
				isync, err := srv.openIS(isID)
				if err != nil {
					opLogger.Error(err.Error(), logMode("is"), logPlayerID(isyncPlayerID), logRandoID(isID))
					return
				}
				defer isync.Close()
				index.Close()
				srv.serveItemSyncGame(conn, logger, isync, isID, isyncPlayerID)
				return
			default:
				opLogger.Info("unknown mode", slog.Int("modenum", int(msg.Mode)), slog.Int("playerID", int(msg.PlayerID)), slog.Int("randoID", int(msg.RandoID)))
			}
		default:
			logger.Info(fmt.Sprintf("unexpected message (awaiting ready): %#v", msg))
		}
	}
}

func readyConfirm(names []string) mwproto.ReadyConfirmMessage {
	return mwproto.ReadyConfirmMessage{
		Ready: int32(len(names)),
		Names: names,
	}
}
