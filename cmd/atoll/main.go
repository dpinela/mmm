package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/mwfile"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/sqlite"
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
	nf := newNotifier()
	go serveConsole(&cfg, workdir, nf)
	return serve(&cfg, workdir, nf)
}

func openMW(workdir string, randoID indexfile.RandoID) (*mwfile.File, error) {
	return mwfile.Open(filepath.Join(workdir, strconv.FormatInt(int64(randoID), 10)+".atollmw"))
}

func openIndex(workdir string) (*indexfile.File, error) {
	return indexfile.Open(filepath.Join(workdir, "index.atolldb"))
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

func serve(cfg *serverConfig, workdir string, nf *notifier) error {
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
		go serveClient(conn, workdir, nf)
	}
}

func serveClient(conn *mwproto.ServerConn, workdir string, nf *notifier) {
	defer conn.Close()

	index, err := openIndex(workdir)
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
		mw          *mwfile.File
		randoID     indexfile.RandoID
		playerID    mwfile.PlayerID
		playerNames []string
	)
	// More than one pending notification is redundant anyway. The sending side will drop
	// redundant ones automatically.
	shuffleNotifications := make(chan struct{}, 1)
	playerChangeNotifications := make(chan struct{}, 1)

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
			randoID, err = index.FindRoom(msg.Room)
			if err == indexfile.ErrRoomNotExist {
				log.Printf("%q tried to access nonexistent room %q", msg.Nickname, msg.Room)
				conn.Send(mwproto.ReadyDenyMessage{Description: "room does not exist"})
				continue
			}
			if err != nil {
				log.Println(err)
				return
			}

			mw, err = openMW(workdir, randoID)
			if errors.Is(err, sqlite.ErrCantOpen) {
				// deleting the file effectively deletes the room
				if err := index.DeleteRoom(randoID); err != nil {
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

			playerID, playerNames, err = mw.Join(msg.Nickname)
			if err != nil {
				log.Println(err)
				return
			}
			nf.playerChangeTopic.Notify(randoID)
			conn.Send(readyConfirm(playerNames))
			conn.Send(mwproto.RequestRandoMessage{})
			break waitingForReadyOrJoin
		case mwproto.ItemSyncReadyMessage:
			log.Printf("%q tried to access ItemSync room %q", msg.Nickname, msg.Room)
			conn.Send(mwproto.ReadyDenyMessage{Description: "ItemSync not supported on this server"})
			continue
		case mwproto.JoinMessage:
			randoID = indexfile.RandoID(msg.RandoID)
			playerID = mwfile.PlayerID(msg.PlayerID)
			mw, err = openMW(workdir, randoID)
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
				serveClientInGame(conn, mw, nf, randoID, playerID)
			} else {
				log.Printf("rando %d isn't shuffled yet", randoID)
			}
			return
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %#v", conn.RemoteAddr(), msg)
		}
	}

	log.Printf("room ID = %d; player ID = %d", randoID, playerID)

	// There is a tiny window from Join to here where a shuffle notification from
	// another goroutine would be lost, but the room wasn't shuffled yet when we checked
	// so we're stuck waiting.
	// In the very unlikely event this happens, the client can disconnect and reconnect
	// to resolve the issue.
	nf.shuffleTopic.Listen(randoID, shuffleNotifications)
	defer nf.shuffleTopic.Mute(randoID, shuffleNotifications)
	nf.playerChangeTopic.Listen(randoID, playerChangeNotifications)
	defer nf.playerChangeTopic.Mute(randoID, playerChangeNotifications)

	var attachedRando *mwproto.RandoGeneratedMessage

	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return
			}

			switch msg := msg.(type) {
			case mwproto.RandoGeneratedMessage:
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					log.Println(err)
					return
				}
				if isShuffled {
					err = sendRandoResult(conn, mw, randoID, playerID, msg)
				} else {
					err = mw.Attach(playerID, msg)
				}
				if err != nil {
					log.Println(err)
					return
				}
			case mwproto.DisconnectMessage:
				log.Printf("connection from %s terminated", conn.RemoteAddr())
				return
			case mwproto.UnreadyMessage:
				goto waitingForReadyOrJoin
			case mwproto.JoinMessage:
				isShuffled, err := mw.IsShuffled()
				if err != nil {
					log.Println(err)
					return
				}
				if !isShuffled {
					log.Printf("%s tried to access room %d, player %d: %v", conn.RemoteAddr(), msg.RandoID, msg.PlayerID, err)
					continue
				}
				serveClientInGame(conn, mw, nf, randoID, playerID)
				return
			default:
				log.Printf("unexpected message (in room) from %s: %#v", conn.RemoteAddr(), msg)
			}
		case <-shuffleNotifications:
			if attachedRando == nil {
				continue
			}
			if err := sendRandoResult(conn, mw, randoID, playerID, *attachedRando); err != nil {
				log.Println(err)
				return
			}
		case <-playerChangeNotifications:
			names, err := mw.PlayerNames()
			if err != nil {
				log.Println(err)
				return
			}
			conn.Send(readyConfirm(names))
		}
	}
}

func readyConfirm(names []string) mwproto.ReadyConfirmMessage {
	return mwproto.ReadyConfirmMessage{
		Ready: int32(len(names)),
		Names: names,
	}
}

func sendRandoResult(conn *mwproto.ServerConn, mw *mwfile.File, randoID indexfile.RandoID, playerID mwfile.PlayerID, currentRando mwproto.RandoGeneratedMessage) error {
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

func serveClientInGame(conn *mwproto.ServerConn, mw *mwfile.File, nf *notifier, randoID indexfile.RandoID, playerID mwfile.PlayerID) {
	conn.Send(mwproto.JoinConfirmMessage{})

	itemNotifications := make(chan struct{}, 1)
	sid := subscriberID{playerID: int64(playerID), randoID: randoID}
	nf.itemTopic.Listen(sid, itemNotifications)
	defer nf.itemTopic.Mute(sid, itemNotifications)

	notchCostNotifications := make(chan struct{}, 1)
	nf.notchCostTopic.Listen(randoID, notchCostNotifications)
	defer nf.notchCostTopic.Mute(randoID, notchCostNotifications)

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
				nf.notchCostTopic.Notify(randoID)
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
				nf.itemTopic.Notify(subscriberID{randoID: randoID, playerID: int64(msg.To)})
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
					nf.itemTopic.Notify(subscriberID{randoID: randoID, playerID: int64(p)})
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
