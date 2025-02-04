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
	"strconv"
	"strings"
	"time"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/pickle"
)

func main() {
	var opts options
	flag.StringVar(&opts.apfile, "apfile", "./AP.archipelago", "The Archipelago seed to serve")
	flag.StringVar(&opts.mwserver, "mwserver", "127.0.0.1:38281", "The multiworld server to join")
	flag.StringVar(&opts.mwroom, "mwroom", "", "The room to join")
	flag.IntVar(&opts.apport, "apport", 38281, "Serve Archipelago on port `port`")
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
	apport   int
}

func serve(opts options) error {
	data, err := readAPFile(opts.apfile)
	if err != nil {
		return err
	}
	if len(data.ConnectNames) != 1 {
		return fmt.Errorf(".archipelago contains %d worlds, expected only one", len(data.ConnectNames))
	}
	if len(data.Version) != approto.VersionNumberSize {
		return fmt.Errorf("invalid .archipelago version: %v", data.Version)
	}
	err = joinServer(opts, data)
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
	ConnectNames  map[string][]int
	Spheres       []map[int][]int
	Locations     map[int]map[int][]int
	Datapackage   map[string]apgamedata
	SlotInfo      map[int]apslot
	SlotData      map[int]map[string]any `pickle:"require_string_keys"`
	Version       []int
	Tags          []string
	ServerOptions apserveroptions
	SeedName      string
}

type apserveroptions struct {
	LocationCheckPoints int
	HintCost            int
	ReleaseMode         string
	CollectMode         string
	RemainingMode       string
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
	Original         map[string]any `pickle:"require_string_keys,remainder"`
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

func joinServer(opts options, data apdata) error {
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

	conn, err := net.Dial("tcp", opts.mwserver)
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
		Room:          opts.mwroom,
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
	}

waitingForStartMW:
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
			case mwproto.ReadyConfirmMessage:
				log.Printf("players in room: %v", msg.Names)
			case mwproto.RequestRandoMessage:
				outbox <- mwproto.RandoGeneratedMessage{
					Items: map[string][]mwproto.Placement{
						singularItemGroup: mwPlacements,
					},
					Seed: 666_666_666,
				}
				break waitingForStartMW
			default:
				log.Printf("unexpected message while in room: %#v", msg)
			}
		}
	}

	var mwResult mwproto.ResultMessage

waitingForResult:
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
			case mwproto.ResultMessage:
				mwResult = msg
				break waitingForResult
			}
		}
	}

	games := make([]string, len(mwResult.Nicknames))
	checksums := make([]string, len(mwResult.Nicknames))
	dataPackages := map[string]*approto.DataPackage{}
	for i, name := range mwResult.Nicknames {
		if i == int(mwResult.PlayerID) {
			games[i] = slot.Game
		} else {
			games[i] = fmt.Sprintf("%s's World", name)
		}
		dataPackages[games[i]] = &approto.DataPackage{
			LocationNameToID: map[string]int{},
			ItemNameToID:     map[string]int{},
		}
	}

	nextSynthItemID := 1
	nextSynthLocationID := 1
	for _, p := range mwResult.Placements[singularItemGroup] {
		pid, item, ok := parseQualifiedName(p.Item)
		if !ok {
			log.Println("invalid MW item:", p.Item)
			continue
		}
		if !(pid >= 0 && pid < len(games)) {
			log.Println("MW item has world out of range:", p.Item)
			continue
		}
		game := games[pid]
		dp := dataPackages[game]
		if _, ok := dp.ItemNameToID[item]; !ok {
			dp.ItemNameToID[item] = nextSynthItemID
			nextSynthItemID++
		}
	}
	for _, qualifiedLoc := range mwResult.PlayerItemsPlacements {
		pid, loc, ok := parseQualifiedName(qualifiedLoc)
		if !ok {
			log.Println("invalid MW location:", qualifiedLoc)
			continue
		}
		if !(pid >= 0 && pid < len(games)) {
			log.Println("MW location has world out of range:", qualifiedLoc)
			continue
		}
		game := games[pid]
		dp := dataPackages[game]
		if _, ok := dp.LocationNameToID[loc]; !ok {
			dp.LocationNameToID[loc] = nextSynthLocationID
			nextSynthLocationID++
		}
	}
	for i, g := range games {
		if i == int(mwResult.PlayerID) {
			checksums[i] = data.Datapackage[slot.Game].Checksum
		} else {
			dp := dataPackages[g]
			dp.SetChecksum()
			checksums[i] = dp.Checksum
		}
	}

	roomInfo := approto.RoomInfo{
		Cmd:     "RoomInfo",
		Version: apServerVersion,
		// This would panic if data.Version is not of the
		// correct length, but we check for this right after
		// loading the .archipelago file.
		GeneratorVersion: approto.MakeVersion(*(*[approto.VersionNumberSize]int)(data.Version)),
		Tags:             data.Tags,
		Password:         false,
		Permissions: approto.RoomPermissions{
			Release:   approto.PermissionForMode(data.ServerOptions.ReleaseMode),
			Collect:   approto.PermissionForMode(data.ServerOptions.CollectMode),
			Remaining: approto.PermissionForMode(data.ServerOptions.RemainingMode),
		},
		HintCost:             data.ServerOptions.HintCost,
		LocationCheckPoints:  data.ServerOptions.LocationCheckPoints,
		Games:                games,
		DataPackageChecksums: checksums,
		SeedName:             data.SeedName,
		// Time will be set by approto.Serve
	}

	var (
		locationsCleared = []int{}
		itemsSent        []approto.NetworkItem
		dataStorage      = apDataStorage{}
	)

	for i := range mwResult.Nicknames {
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"hints_0_%d", i+1)] = []any{}
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"client_status_0_%d", i+1)] = approto.ClientStatusUnknown
		itemGroupsKey := approto.ReadOnlyKeyPrefix + "item_name_groups_%s" + games[i]
		locationGroupsKey := approto.ReadOnlyKeyPrefix + "location_name_groups_" + games[i]
		if i == int(mwResult.PlayerID) {
			dpkg := data.Datapackage[slot.Game]
			dataStorage[itemGroupsKey] = dpkg.Original["item_name_groups"]
			dataStorage[locationGroupsKey] = dpkg.Original["location_name_groups"]
		} else {
			dataStorage[itemGroupsKey] = map[string][]string{}
			dataStorage[locationGroupsKey] = map[string][]string{}
		}
	}
	dataStorage[approto.ReadOnlyKeyPrefix+"race_mode"] = 0
	dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"slot_data_%d", slotID)] = data.SlotData[slotID]
	for i := range mwResult.Nicknames {
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"slot_data_%d", slotID+i+1)] = map[string]any{}
	}

	apInbox, apOutbox := approto.Serve(opts.apport, roomInfo)

mainMessageLoop:
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
			case mwproto.DataReceiveMessage:
				if msg.Label != mwproto.LabelMultiworldItem {
					log.Println("unknown label for received item:", msg.Label)
					continue
				}
				if !(msg.FromID >= 0 && int(msg.FromID) < len(games)) {
					log.Println("invalid FromID:", msg.FromID)
					continue
				}
				ownPkg := dataPackages[games[mwResult.PlayerID]]
				itemID := ownPkg.ItemNameToID[msg.Content]
				locID := 0
				if loc, ok := mwResult.PlayerItemsPlacements[msg.Content]; ok {
					_, loc, ok = parseQualifiedName(loc)
					if ok {
						fromPkg := dataPackages[games[msg.FromID]]
						locID = fromPkg.LocationNameToID[loc]
					}
				}
				ni := approto.NetworkItem{
					Item:     itemID,
					Location: locID,
					Player:   int(msg.FromID),
					Flags:    0,
				}
				apOutbox <- approto.ReceivedItems{
					Index: len(itemsSent),
					Items: []approto.NetworkItem{ni},
				}
				itemsSent = append(itemsSent, ni)
			}
		case msg := <-apInbox:
			if msg == nil {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case approto.GetDataPackage:
				resp := approto.MakeDataPackageMessage()
				pickedGames := msg.Games
				if pickedGames == nil {
					pickedGames = games
				}
				for _, g := range pickedGames {
					if g == slot.Game {
						resp.Data.Games[g] = data.Datapackage[slot.Game]
					} else {
						resp.Data.Games[g] = dataPackages[g]
					}
				}
				apOutbox <- resp
			case approto.Connect:
				outbox <- mwproto.JoinMessage{
					PlayerID: mwResult.PlayerID,
					RandoID:  mwResult.RandoID,
				}
				players := make([]approto.NetworkPlayer, len(mwResult.Nicknames))
				slots := make(map[int]approto.NetworkSlot, len(mwResult.Nicknames))
				nextSlot := slotID + 1
				for i, nick := range mwResult.Nicknames {
					slot := slotID
					if i != int(mwResult.PlayerID) {
						slot = nextSlot
						nextSlot++
					}
					players[i] = approto.NetworkPlayer{
						Team:  0,
						Slot:  slot,
						Alias: nick,
						Name:  nick,
					}
					slots[slot] = approto.NetworkSlot{
						Name:         nick,
						Game:         games[i],
						Type:         approto.SlotTypePlayer,
						GroupMembers: []int{},
					}
				}
				var missingLocations []int
				for _, locID := range data.Datapackage[slot.Game].LocationNameToID {
					missingLocations = append(missingLocations, locID)
				}
				resp := approto.Connected{
					Cmd:              "Connected",
					Team:             0,
					Slot:             slotID,
					Players:          players,
					SlotInfo:         slots,
					CheckedLocations: locationsCleared,
					MissingLocations: missingLocations,
					HintPoints:       0,
				}
				if msg.SlotData {
					resp.SlotData = data.SlotData[slotID]
				}
				apOutbox <- resp
			case approto.SetMessage:
				oldV, newV, err := dataStorage.apply(msg)
				if err != nil {
					log.Println(err)
					continue mainMessageLoop
				}
				if msg.WantReply {
					apOutbox <- approto.SetReplyMessage{
						Cmd:           "SetReply",
						Key:           msg.Key,
						Value:         newV,
						OriginalValue: oldV,
						Slot:          slotID,
					}
				}
			case approto.GetMessage:
				values := make(map[string]any, len(msg.Keys))
				for _, k := range msg.Keys {
					values[k] = dataStorage[k]
				}
				apOutbox <- approto.RetrievedMessage{
					Keys: values,
					Rest: msg.Rest,
				}
			}
		}
	}
}

func parseQualifiedName(name string) (pid int, item string, ok bool) {
	const prefix = "MW("

	if !strings.HasPrefix(name, prefix) {
		return
	}
	qualifier, item, ok := strings.Cut(name[len(prefix):], ")_")
	if !ok {
		return
	}
	n, err := strconv.ParseInt(qualifier, 10, 32)
	if err != nil {
		ok = false
		return
	}
	return int(n), item, ok
}

// This is the main item group used by the HK rando as well as
// the sole item group used by Haiku and Death's Door for multiworld
// purposes.
const singularItemGroup = "Main Item Group"

const (
	fakeLocationName = "Somewhere"
	fakeLocationID   = 1
)

var apServerVersion = approto.Version{
	Minor: 5,
	Build: 1,
	Class: "Version",
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
