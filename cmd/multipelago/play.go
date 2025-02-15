package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
)

func playMW(opts options, data apdata) error {
	state, err := openPersistentState(filepath.Join(opts.workdir, "state.sqlite3"))
	if err != nil {
		return fmt.Errorf("open persistent state DB: %w", err)
	}
	defer state.close()

	mwResult, err := readMWResult(opts.workdir)
	if err != nil {
		return err
	}
	if len(data.SlotInfo) != 1 {
		return fmt.Errorf(".archipelago contains %d slots, expected only one", len(data.SlotInfo))
	}
	conn, err := mwproto.Dial(opts.mwserver)
	if err != nil {
		return fmt.Errorf("connect to MW: %w", err)
	}
	defer conn.Close()

	conn.Send(mwproto.ConnectMessage{})

	slotID := singularKey(data.SlotInfo)
	slot := data.SlotInfo[slotID]

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
			LocationNameToID: map[string]int64{},
			ItemNameToID:     map[string]int64{},
		}
	}

	placementsByLocationID := map[int64]placedItem{}

	nextSynthItemID := int64(1)
	nextSynthLocationID := int64(1)
	prettyNames := prettifyItemNames(mwResult.Placements[singularItemGroup], mwResult.PlayerID)
	for _, p := range mwResult.Placements[singularItemGroup] {
		pid, item, ok := mwproto.ParseQualifiedName(p.Item)
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
		prettyItem := prettyNames[item]
		if _, ok := dp.ItemNameToID[prettyItem]; !ok {
			dp.ItemNameToID[prettyItem] = nextSynthItemID
			nextSynthItemID++
		}
		if locID, ok := mwproto.ParseDiscriminator(p.Location); ok {
			placementsByLocationID[locID] = placedItem{ownerID: pid, name: item}
		} else {
			log.Println("location without discriminator:", p.Location)
		}
	}
	// Map iteration order is randomized, but we need the LocationNameToID mappings
	// to be consistent across runs.
	for _, qualifiedLoc := range slices.Sorted(maps.Values(mwResult.PlayerItemsPlacements)) {
		pid, loc, ok := mwproto.ParseQualifiedName(qualifiedLoc)
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
		itemHandling     approto.ItemHandlingMode
		lastReceivedItem = ""
		dataStorage      = map[string]any{}
		watchedKeys      = map[string]struct{}{}
	)

	for i := range mwResult.Nicknames {
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"hints_0_%d", i+1)] = []any{}
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"client_status_0_%d", i+1)] = approto.ClientStatusUnknown
		itemGroupsKey := approto.ReadOnlyKeyPrefix + "item_name_groups_" + games[i]
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
	for i := range mwResult.Nicknames {
		key := fmt.Sprintf(approto.ReadOnlyKeyPrefix+"slot_data_%d", i+1)
		if i == int(mwResult.PlayerID) {
			dataStorage[key] = data.SlotData[slotID]
		} else {
			dataStorage[key] = map[string]any{}
		}
	}

	apInbox, apOutbox := approto.Serve(opts.apport, roomInfo)

mainMessageLoop:
	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.DataReceiveMessage:
				if msg.Label != mwproto.LabelMultiworldItem {
					log.Println("unknown label for received item:", msg.Label)
					continue
				}
				if !(msg.FromID >= 0 && int(msg.FromID) < len(games)) {
					log.Println("invalid FromID:", msg.FromID)
					continue
				}
				if msg.Content == lastReceivedItem {
					log.Printf("ignoring duplicate item %q from %q", msg.Content, msg.From)
					continue
				}
				lastReceivedItem = msg.Content
				ownPkg := data.Datapackage[slot.Game]
				itemID := ownPkg.ItemNameToID[mwproto.StripDiscriminator(msg.Content)]
				var locID int64
				if loc, ok := mwResult.PlayerItemsPlacements[msg.Content]; ok {
					_, loc, ok = mwproto.ParseQualifiedName(loc)
					if ok {
						fromPkg := dataPackages[games[msg.FromID]]
						locID = fromPkg.LocationNameToID[loc]
					}
				}
				ni := approto.NetworkItem{
					Item:     itemID,
					Location: locID,
					Player:   int(msg.FromID) + 1,
					Flags:    0,
				}
				// TODO: send Save messages
				// TODO: try to resend unconfirmed sent items
				// TODO: Sync
				// TODO: return already-sent AP items on connect
				index, err := state.addSentItem(ni)
				if err != nil {
					return err
				}
				apOutbox <- approto.ReceivedItems{
					Cmd:   "ReceivedItems",
					Index: index,
					Items: []approto.NetworkItem{ni},
				}
				conn.Send(mwproto.DataReceiveConfirmMessage{
					Label: msg.Label,
					Data:  msg.Content,
					From:  msg.From,
				})
			case mwproto.DataSendConfirmMessage:
				confirmed, err := state.confirmItem(msg)
				if err != nil {
					return err
				}
				if !confirmed {
					log.Printf("received confirmation for item that wasn't sent: label=%q content=%q to=%d", msg.Label, msg.Content, msg.To)
				}
			case mwproto.RequestCharmNotchCostsMessage:
				// We have nothing to announce.
				conn.Send(mwproto.AnnounceCharmNotchCostsMessage{
					PlayerID:   mwResult.PlayerID,
					NotchCosts: map[int]int{},
				})
			case mwproto.AnnounceCharmNotchCostsMessage:
				log.Println("got charm notch costs for player", msg.PlayerID)
				for charm := range slices.Sorted(maps.Keys(msg.NotchCosts)) {
					log.Println("charm", charm, "costs", msg.NotchCosts[charm], "notches")
				}
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
						resp.Data.Games[g] = data.Datapackage[slot.Game].Original
					} else {
						resp.Data.Games[g] = dataPackages[g]
					}
				}
				apOutbox <- resp
			case approto.Connect:
				conn.Send(mwproto.JoinMessage{
					DisplayName: slot.Name,
					PlayerID:    mwResult.PlayerID,
					RandoID:     mwResult.RandoID,
				})
				players := make([]approto.NetworkPlayer, len(mwResult.Nicknames))
				slots := make(map[int]approto.NetworkSlot, len(mwResult.Nicknames))
				for i, nick := range mwResult.Nicknames {
					slot := i + 1
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
				missingLocationSet := map[int64]struct{}{}
				for _, locID := range data.Datapackage[slot.Game].LocationNameToID {
					missingLocationSet[locID] = struct{}{}
				}
				checkedLocations, err := state.clearedLocations()
				if err != nil {
					return err
				}
				for _, locID := range checkedLocations {
					delete(missingLocationSet, locID)
				}
				if msg.ItemsHandling == nil {
					itemHandling = approto.ReceiveOthersItems
				} else {
					itemHandling = *msg.ItemsHandling
				}
				itemHandling = *msg.ItemsHandling
				resp := approto.Connected{
					Cmd:              "Connected",
					Team:             0,
					Slot:             int(mwResult.PlayerID) + 1,
					Players:          players,
					SlotInfo:         slots,
					CheckedLocations: checkedLocations,
					MissingLocations: slices.Sorted(maps.Keys(missingLocationSet)),
					HintPoints:       0,
				}
				if msg.SlotData {
					resp.SlotData = data.SlotData[slotID]
				}
				apOutbox <- resp

				items, err := state.getSentItems()
				if err != nil {
					return err
				}

				apOutbox <- approto.ReceivedItems{Index: 0, Items: items}
			case approto.SyncMessage:
				items, err := state.getSentItems()
				if err != nil {
					return err
				}

				apOutbox <- approto.ReceivedItems{Index: 0, Items: items}
			case approto.SetMessage:
				oldV, newV, err := updateDataStorage(state, msg)
				if err != nil {
					log.Println(err)
					continue mainMessageLoop
				}
				_, watching := watchedKeys[msg.Key]
				if msg.WantReply || watching {
					apOutbox <- approto.SetReplyMessage{
						Cmd:           "SetReply",
						Key:           msg.Key,
						Value:         newV,
						OriginalValue: oldV,
						Slot:          int(mwResult.PlayerID),
					}
				}
			case approto.SetNotifyMessage:
				for _, k := range msg.Keys {
					log.Println("client watching key", k)
					watchedKeys[k] = struct{}{}
				}
			case approto.GetMessage:
				values := make(map[string]any, len(msg.Keys))
				for _, k := range msg.Keys {
					if strings.HasPrefix(k, approto.ReadOnlyKeyPrefix) {
						values[k] = dataStorage[k]
						continue
					}
					val, found, err := state.getStoredData(k)
					if err != nil {
						return err
					}
					if found {
						values[k] = json.RawMessage(val)
					} else {
						values[k] = nil
					}
				}
				apOutbox <- approto.MakeRetrievedMessage(values, msg.Rest)
			case approto.LocationScoutsMessage:
				scoutedItems := make([]approto.NetworkItem, 0, len(msg.Locations))
				for _, locID := range msg.Locations {
					p, ok := placementsByLocationID[locID]
					if ok {
						var itemID int64
						if p.ownerID == int(mwResult.PlayerID) {
							itemID = data.Datapackage[slot.Game].ItemNameToID[mwproto.StripDiscriminator(p.name)]
						} else {
							name := prettyNames[p.name]
							itemID = dataPackages[games[p.ownerID]].ItemNameToID[name]
						}
						scoutedItems = append(scoutedItems, approto.NetworkItem{
							Location: locID,
							Player:   p.ownerID + 1,
							Item:     itemID,
							Flags:    0,
						})
					} else {
						ownItem, ok := data.Locations[slotID][locID]
						if !(ok && len(ownItem) >= 3) {
							continue
						}
						scoutedItems = append(scoutedItems, approto.NetworkItem{
							Location: locID,
							Player:   int(mwResult.PlayerID) + 1,
							Item:     ownItem[0],
							Flags:    int(ownItem[2]),
						})
					}
				}
				apOutbox <- approto.LocationInfoMessage{
					Cmd:       "LocationInfo",
					Locations: scoutedItems,
				}
			case approto.LocationChecksMessage:
				for _, locID := range msg.Locations {
					checked, err := state.isLocationCleared(locID)
					if err != nil {
						return err
					}
					if checked {
						continue
					}

					if p, replaced := placementsByLocationID[locID]; replaced {
						if p.ownerID == int(mwResult.PlayerID) {
							if itemHandling&approto.ReceiveOwnItems == 0 {
								continue
							}
							name := mwproto.StripDiscriminator(p.name)
							itemID := data.Datapackage[slot.Game].ItemNameToID[name]
							item := approto.NetworkItem{
								Location: locID,
								Player:   int(mwResult.PlayerID) + 1,
								Item:     itemID,
								Flags:    0,
							}
							index, err := state.addSentItem(item)
							if err != nil {
								return err
							}
							apOutbox <- approto.ReceivedItems{
								Cmd:   "ReceivedItems",
								Index: index,
								Items: []approto.NetworkItem{item},
							}
						} else {
							msg := mwproto.DataSendMessage{
								Label:   mwproto.LabelMultiworldItem,
								Content: p.name,
								To:      int32(p.ownerID),
								TTL:     666,
							}
							if err := state.addUnconfirmedItem(msg); err != nil {
								return err
							}
							conn.Send(msg)
						}
					} else {
						if itemHandling&approto.ReceiveOwnItems == 0 {
							continue
						}
						ownItem, ok := data.Locations[slotID][locID]
						if !(ok && len(ownItem) >= 3) {
							continue
						}
						item := approto.NetworkItem{
							Location: locID,
							Player:   int(mwResult.PlayerID + 1),
							Item:     ownItem[0],
							Flags:    int(ownItem[2]),
						}
						index, err := state.addSentItem(item)
						if err != nil {
							return err
						}
						apOutbox <- approto.ReceivedItems{
							Cmd:   "ReceivedItems",
							Index: index,
							Items: []approto.NetworkItem{item},
						}
					}

					if err := state.clearLocation(locID); err != nil {
						return err
					}
				}
			}
		}
	}
}
