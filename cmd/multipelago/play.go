package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
)

func playMW(opts options, data apdata) error {
	server := approto.Serve(opts.apport)
	defer server.Close()
	for {
		conn := server.Accept()
		err := playMWWithConn(opts, data, conn)
		if err == errConnectionLost {
			continue
		}
		if err != nil {
			return err
		}
	}
}

func playMWWithConn(opts options, data apdata, apconn *approto.ClientConn) error {
	defer apconn.Close()
	state, err := openSavefile(opts.savefile)
	if err != nil {
		return fmt.Errorf("open persistent state DB: %w", err)
	}
	defer state.close()

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

	nicknames, err := state.getNicknames()
	if err != nil {
		return err
	}
	playerID, randoID, err := state.getConnectionParams()
	if err != nil {
		return err
	}

	games := make([]string, len(nicknames))
	checksums := make([]string, len(nicknames))
	dataPackages := map[string]*approto.DataPackage{}
	for i, name := range nicknames {
		if i == playerID {
			games[i] = slot.Game
		} else {
			games[i] = fmt.Sprintf("%s's World", name)
		}
		dataPackages[games[i]] = &approto.DataPackage{
			LocationNameToID: map[string]int64{},
			ItemNameToID:     map[string]int64{},
		}
	}

	nextSynthItemID := int64(1)
	nextSynthLocationID := int64(1)
	for p, err := range state.getOwnWorldPlacements() {
		if err != nil {
			return err
		}
		if !(p.ownerID >= 0 && p.ownerID < len(games)) {
			log.Println("MW item has world out of range:", p.placedItem.name)
			continue
		}
		game := games[p.ownerID]
		dp := dataPackages[game]
		prettyItem := strings.ReplaceAll(mwproto.StripDiscriminator(p.placedItem.name), "_", " ")
		if _, ok := dp.ItemNameToID[prettyItem]; !ok {
			dp.ItemNameToID[prettyItem] = nextSynthItemID
			nextSynthItemID++
		}
	}

	for qualifiedLoc, err := range state.getOwnItemLocations() {
		if err != nil {
			return err
		}
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
		if i == playerID {
			checksums[i] = data.Datapackage[slot.Game].Checksum
		} else {
			dp := dataPackages[g]
			dp.SetChecksum()
			checksums[i] = dp.Checksum
		}
	}

	apconn.Send(approto.RoomInfo{
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
		Time:                 float64(time.Now().UnixMilli()) / float64(time.Millisecond),
	})

	var (
		itemHandling approto.ItemHandlingMode
		dataStorage  = map[string]any{}
		watchedKeys  = map[string]struct{}{}
	)

	for i := range nicknames {
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"hints_0_%d", i+1)] = []any{}
		dataStorage[fmt.Sprintf(approto.ReadOnlyKeyPrefix+"client_status_0_%d", i+1)] = approto.ClientStatusUnknown
		itemGroupsKey := approto.ReadOnlyKeyPrefix + "item_name_groups_" + games[i]
		locationGroupsKey := approto.ReadOnlyKeyPrefix + "location_name_groups_" + games[i]
		if i == playerID {
			dpkg := data.Datapackage[slot.Game]
			dataStorage[itemGroupsKey] = dpkg.Original["item_name_groups"]
			dataStorage[locationGroupsKey] = dpkg.Original["location_name_groups"]
		} else {
			dataStorage[itemGroupsKey] = map[string][]string{}
			dataStorage[locationGroupsKey] = map[string][]string{}
		}
	}
	dataStorage[approto.ReadOnlyKeyPrefix+"race_mode"] = 0
	for i := range nicknames {
		key := fmt.Sprintf(approto.ReadOnlyKeyPrefix+"slot_data_%d", i+1)
		if i == playerID {
			dataStorage[key] = data.SlotData[slotID]
		} else {
			dataStorage[key] = map[string]any{}
		}
	}

mainMessageLoop:
	for {
		select {
		case msg, ok := <-conn.Inbox():
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.JoinConfirmMessage:
				unconfirmedItems, err := state.getUnconfirmedItems()
				if err != nil {
					return err
				}
				log.Println("resending", len(unconfirmedItems), "unconfirmed items")
				for _, it := range unconfirmedItems {
					conn.Send(it)
				}
			case mwproto.DataReceiveMessage:
				if msg.Label != mwproto.LabelMultiworldItem {
					log.Println("unknown label for received item:", msg.Label)
					continue
				}
				if !(msg.FromID >= 0 && int(msg.FromID) < len(games)) {
					log.Println("invalid FromID:", msg.FromID)
					continue
				}
				duplicate, err := state.hasReceivedItem(msg.Label, msg.Content)
				if err != nil {
					return err
				}
				if duplicate {
					log.Printf("ignoring duplicate item %q from %q", msg.Content, msg.From)
					continue
				}
				ownPkg := data.Datapackage[slot.Game]
				itemID := ownPkg.ItemNameToID[mwproto.StripDiscriminator(msg.Content)]
				var locID int64
				loc, err := state.getLocationOfOwnItem(msg.Content)
				if err == nil {
					_, loc, ok = mwproto.ParseQualifiedName(loc)
					if ok {
						fromPkg := dataPackages[games[msg.FromID]]
						locID = fromPkg.LocationNameToID[loc]
					}
				} else if err != errZeroRows {
					return err
				}
				ni := approto.NetworkItem{
					Item:     itemID,
					Location: locID,
					Player:   int(msg.FromID) + 1,
					Flags:    0,
				}
				index, err := state.addSentItem(ni)
				if err != nil {
					return err
				}
				log.Printf("received %s from player %d (%s); AP index %d", msg.Content, msg.FromID, msg.From, index)
				apconn.Send(approto.ReceivedItems{
					Cmd:   "ReceivedItems",
					Index: index,
					Items: []approto.NetworkItem{ni},
				})
				conn.Send(mwproto.DataReceiveConfirmMessage{
					Label: msg.Label,
					Data:  msg.Content,
					From:  msg.From,
				})
				err = state.addReceivedItem(msg.Label, msg.Content)
				if err != nil {
					return err
				}
				conn.Send(mwproto.SaveMessage{})
			case mwproto.DatasReceiveMessage:
				fromID := slices.Index(nicknames, msg.From)
				if fromID == -1 {
					log.Println("receiving released items from unknown player", msg.From)
				}
				startIndex := -1
				items := make([]approto.NetworkItem, 0, len(msg.Items))
				for _, item := range msg.Items {
					if item.Label != mwproto.LabelMultiworldItem {
						log.Println("unknown label for received item:", item.Label)
						continue
					}
					duplicate, err := state.hasReceivedItem(item.Label, item.Content)
					if err != nil {
						return err
					}
					if duplicate {
						log.Printf("ignoring duplicate item %q from %q", item.Content, msg.From)
						continue
					}
					ownPkg := data.Datapackage[slot.Game]
					itemID := ownPkg.ItemNameToID[mwproto.StripDiscriminator(item.Content)]
					sentItem := approto.NetworkItem{
						Item:  itemID,
						Flags: 0,
					}
					if fromID == -1 {
						sentItem.Location = -2
						sentItem.Player = 0
					} else {
						sentItem.Player = fromID + 1
						loc, err := state.getLocationOfOwnItem(item.Content)
						if err == nil {
							_, loc, ok = mwproto.ParseQualifiedName(loc)
							if ok {
								fromPkg := dataPackages[games[fromID]]
								sentItem.Location = fromPkg.LocationNameToID[loc]
							}
						} else if err != errZeroRows {
							return err
						}
					}
					items = append(items, sentItem)
					index, err := state.addSentItem(sentItem)
					if err != nil {
						return err
					}
					if startIndex == -1 {
						startIndex = index
					}
					err = state.addReceivedItem(item.Label, item.Content)
					if err != nil {
						return err
					}
				}
				log.Printf("received %d released items from %s", len(items), msg.From)
				apconn.Send(approto.ReceivedItems{
					Cmd:   "ReceivedItems",
					Index: startIndex,
					Items: items,
				})
				conn.Send(mwproto.DatasReceiveConfirmMessage{
					Count: int32(len(msg.Items)),
					From:  msg.From,
				})
				conn.Send(mwproto.SaveMessage{})
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
					PlayerID:   int32(playerID),
					NotchCosts: map[int]int{},
				})
			case mwproto.AnnounceCharmNotchCostsMessage:
				log.Println("got charm notch costs for player", msg.PlayerID)
				for charm := range slices.Sorted(maps.Keys(msg.NotchCosts)) {
					log.Println("charm", charm, "costs", msg.NotchCosts[charm], "notches")
				}
				conn.Send(mwproto.ConfirmCharmNotchCostsReceived{
					PlayerID: msg.PlayerID,
				})
			}
		case msg := <-apconn.Inbox():
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
				apconn.Send(resp)
			case approto.Connect:
				conn.Send(mwproto.JoinMessage{
					DisplayName: slot.Name,
					PlayerID:    int32(playerID),
					RandoID:     int32(randoID),
				})
				players := make([]approto.NetworkPlayer, len(nicknames))
				slots := make(map[int]approto.NetworkSlot, len(nicknames))
				for i, nick := range nicknames {
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
					Slot:             playerID + 1,
					Players:          players,
					SlotInfo:         slots,
					CheckedLocations: checkedLocations,
					MissingLocations: slices.Sorted(maps.Keys(missingLocationSet)),
					HintPoints:       0,
				}
				if msg.SlotData {
					resp.SlotData = data.SlotData[slotID]
				}
				apconn.Send(resp)

				items, err := state.getSentItems()
				if err != nil {
					return err
				}

				log.Println("connected to game; sending", len(items), "items")

				apconn.Send(approto.ReceivedItems{
					Cmd:   "ReceivedItems",
					Index: 0,
					Items: items,
				})
			case approto.SyncMessage:
				log.Println("syncing")
				items, err := state.getSentItems()
				if err != nil {
					return err
				}

				apconn.Send(approto.ReceivedItems{
					Cmd:   "ReceivedItems",
					Index: 0,
					Items: items,
				})
			case approto.SetMessage:
				oldV, newV, err := updateDataStorage(state, msg)
				if err != nil {
					log.Println(err)
					continue mainMessageLoop
				}
				_, watching := watchedKeys[msg.Key]
				if msg.WantReply || watching {
					apconn.Send(approto.SetReplyMessage{
						Cmd:           "SetReply",
						Key:           msg.Key,
						Value:         newV,
						OriginalValue: oldV,
						Slot:          playerID,
					})
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
				apconn.Send(approto.MakeRetrievedMessage(values, msg.Rest))
			case approto.LocationScoutsMessage:
				scoutedItems := make([]approto.NetworkItem, 0, len(msg.Locations))
				for _, locID := range msg.Locations {
					p, err := state.getPlacedItem(locID)
					if err != nil && err != errZeroRows {
						return err
					}
					if err == nil {
						var itemID int64
						if p.ownerID == playerID {
							itemID = data.Datapackage[slot.Game].ItemNameToID[mwproto.StripDiscriminator(p.name)]
						} else {
							name := prettifyName(p.name)
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
							Player:   playerID + 1,
							Item:     ownItem[0],
							Flags:    int(ownItem[2]),
						})
					}
				}
				apconn.Send(approto.LocationInfoMessage{
					Cmd:       "LocationInfo",
					Locations: scoutedItems,
				})
			case approto.LocationChecksMessage:
				for _, locID := range msg.Locations {
					checked, err := state.isLocationCleared(locID)
					if err != nil {
						return err
					}
					if checked {
						continue
					}

					p, err := state.getPlacedItem(locID)
					if err != nil && err != errZeroRows {
						return err
					}

					if err == nil {
						if p.ownerID == playerID {
							if itemHandling&approto.ReceiveOwnItems == 0 {
								continue
							}
							name := mwproto.StripDiscriminator(p.name)
							itemID := data.Datapackage[slot.Game].ItemNameToID[name]
							item := approto.NetworkItem{
								Location: locID,
								Player:   playerID + 1,
								Item:     itemID,
								Flags:    0,
							}
							index, err := state.addSentItem(item)
							if err != nil {
								return err
							}
							apconn.Send(approto.ReceivedItems{
								Cmd:   "ReceivedItems",
								Index: index,
								Items: []approto.NetworkItem{item},
							})
						} else {
							msg := mwproto.DataSendMessage{
								Label:   mwproto.LabelMultiworldItem,
								Content: p.name,
								To:      int32(p.ownerID),
								TTL:     sentItemTTL,
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
							Player:   playerID + 1,
							Item:     ownItem[0],
							Flags:    int(ownItem[2]),
						}
						index, err := state.addSentItem(item)
						if err != nil {
							return err
						}
						apconn.Send(approto.ReceivedItems{
							Cmd:   "ReceivedItems",
							Index: index,
							Items: []approto.NetworkItem{item},
						})
					}

					if err := state.clearLocation(locID); err != nil {
						return err
					}
				}
			}
		}
	}
}

func prettifyName(name string) string {
	return strings.ReplaceAll(mwproto.StripDiscriminator(name), "_", " ")
}

const sentItemTTL = 666
