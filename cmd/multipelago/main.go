package main

import (
	"bufio"
	"compress/zlib"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/pickle"
)

func main() {
	var opts options
	flag.StringVar(&opts.apfile, "apfile", "./AP.archipelago", "The Archipelago seed to serve")
	flag.StringVar(&opts.mwserver, "mwserver", "127.0.0.1:38281", "The multiworld server to join")
	flag.StringVar(&opts.mwroom, "mwroom", "", "The room to join")
	flag.Parse()

	if err := serve(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	apfile   string
	mwserver string
	mwroom   string
}

func serve(opts options) error {
	data, err := readAPFile(opts.apfile)
	if err != nil {
		return err
	}
	if len(data.ConnectNames) != 1 {
		return fmt.Errorf(".archipelago contains %d worlds, expected only one", len(data.ConnectNames))
	}
	err = joinServer(opts.mwserver, opts.mwroom, data)
	return err
}

func singularKey[K comparable, V any](m map[K]V) K {
	for k := range m {
		return k
	}
	panic("singularKey undefined on empty map")
}

func invert[K, V comparable](m map[K]V, errmsg string) (map[V]K, error) {
	w := make(map[V]K, len(m))
	for k, v := range m {
		if _, isDup := w[v]; isDup {
			return nil, fmt.Errorf("%s: %v", errmsg, v)
		}
		w[v] = k
	}
	return w, nil
}

type apdata struct {
	ConnectNames map[string][]int
	Spheres      []map[int][]int
	Locations    map[int]map[int][]int
	Datapackage  map[string]apgamedata
	SlotInfo     map[int]apslot
}

type apslot struct {
	Name string
	Game string
	Type struct {
		Code int
	}
	GroupMembers []string
}

type apgamedata struct {
	ItemNameToID     map[string]int
	LocationNameToID map[string]int
	Checksum         string
}

func readAPFile(name string) (data apdata, err error) {
	const expectedAPFileVersion = 3

	apfile, err := os.Open(name)
	if err != nil {
		return
	}
	defer apfile.Close()
	r := bufio.NewReader(apfile)
	version, err := r.ReadByte()
	if err != nil {
		err = fmt.Errorf("read .archipelago version: %w", err)
		return
	}
	if version != expectedAPFileVersion {
		err = fmt.Errorf(".archipelago file is version %d, expected %d", version, expectedAPFileVersion)
		return
	}
	zr, err := zlib.NewReader(r)
	if err != nil {
		err = fmt.Errorf("decompress .archipelago: %w", err)
		return
	}
	defer zr.Close()
	err = pickle.Decode(zr, &data)
	return
}

var errConnectionLost = errors.New("server stopped responding to pings")

func joinServer(mwserver, mwroom string, data apdata) error {
	if len(data.SlotInfo) != 1 {
		return fmt.Errorf(".archipelago contains %d slots, expected only one", len(data.SlotInfo))
	}
	slotID := singularKey(data.SlotInfo)
	slot := data.SlotInfo[slotID]
	nickname := slot.Name
	dpkg, ok := data.Datapackage[slot.Game]
	if !ok {
		return fmt.Errorf(".archipelago does not contain datapackage for main game %s", slot.Game)
	}
	itemNames, err := invert(dpkg.ItemNameToID, "duplicate item ID in datapackage")
	if err != nil {
		return err
	}
	locationNames, err := invert(dpkg.LocationNameToID, "duplicate location ID in datapackage")
	if err != nil {
		return err
	}
	placements, ok := data.Locations[slotID]
	if !ok {
		return errors.New(".archipelago does not contain location data for its single slot")
	}

	for i, s := range data.Spheres {
		fmt.Println("SPHERE", i)
		for _, loc := range s[slotID] {
			locName, ok := locationNames[loc]
			if !ok {
				locName = "Mystery_Place"
			}
			locName = fmt.Sprintf("%s_(%d)", locName, loc)
			p, ok := placements[loc]
			if !ok {
				fmt.Println("\tNOTHING @", locName)
				continue
			}
			if len(p) < 2 {
				fmt.Println("\tMISSING DATA @", locName)
				continue
			}
			itemName, ok := itemNames[p[0]]
			if !ok {
				itemName = "Mystery_Item"
			}
			itemName = fmt.Sprintf("%s_(%d)", itemName, p[0])
			fmt.Printf("\t%s @ %s\n", itemName, locName)
		}
	}

	conn, err := net.Dial("tcp", mwserver)
	if err != nil {
		return err
	}
	defer conn.Close()

	const (
		pingInterval       = 5 * time.Second
		reconnectThreshold = 5
	)

	kill := make(chan struct{})
	defer close(kill)
	outbox := make(chan mwproto.Message)
	defer func() {
		outbox <- mwproto.DisconnectMessage{}
		close(outbox)
	}()
	inbox := make(chan mwproto.Message)

	go sendMessages(conn, outbox)
	go readMessages(conn, inbox, kill)

	outbox <- mwproto.ConnectMessage{}

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

	outbox <- mwproto.ReadyMessage{
		Room:          mwroom,
		Nickname:      nickname,
		ReadyMetadata: []mwproto.KeyValuePair{},
	}

	pingTimer := time.NewTicker(pingInterval)
	defer pingTimer.Stop()
	unansweredPings := 0
waitingToEnterRoom:
	for {
		select {
		case <-pingTimer.C:
			unansweredPings++
			if unansweredPings == reconnectThreshold {
				return errConnectionLost
			}
			outbox <- mwproto.PingMessage{}
		case msg, ok := <-inbox:
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.PingMessage:
				unansweredPings = 0
			case mwproto.ReadyConfirmMessage:
				log.Printf("joined room %s with players %v", mwroom, msg.Names)
				break waitingToEnterRoom
			case mwproto.ReadyDenyMessage:
				log.Printf("denied entry to room %s: %s", mwroom, msg.Description)
			case mwproto.DisconnectMessage:
				return errConnectionLost
			case mwproto.RequestRandoMessage:
			default:
				log.Printf("unexpected message while joining room: %#v", msg)
			}
		}
	}

	for {
		select {
		case <-pingTimer.C:
			unansweredPings++
			if unansweredPings == reconnectThreshold {
				return errConnectionLost
			}
			outbox <- mwproto.PingMessage{}
		case msg, ok := <-inbox:
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.PingMessage:
				log.Println("ping")
				unansweredPings = 0
			case mwproto.DisconnectMessage:
				return errConnectionLost
			default:
				log.Printf("unexpected message while in room: %#v", msg)
			}
		}
	}
}

func sendMessages(conn net.Conn, messages <-chan mwproto.Message) {
	for m := range messages {
		if err := mwproto.Write(conn, m); err != nil {
			log.Println("error sending message:", err)
		}
	}
}

func readMessages(conn net.Conn, messages chan<- mwproto.Message, done <-chan struct{}) {
	for {
		msg, err := mwproto.Read(conn)
		if errors.Is(err, io.EOF) {
			close(messages)
			return
		}
		if err != nil {
			log.Println("error reading message:", err)
			continue
		}
		select {
		case <-done:
			return
		case messages <- msg:
		}
	}
}
