package mwproto

import (
	"encoding/binary"
)

type messageType uint32

const (
	typeInvalid    messageType = iota
	typeSharedCore             // unused
	typeConnect
	typeDisconnect
	typeJoin
	typeJoinConfirm
	typeDataReceive
	typeDataReceiveConfirm
	typeDataSend
	typeDataSendConfirm
	typeReadyConfirm
	typeReadyDeny
	typePing
	typeReady
	typeResult
	typeSave
	typeRandoGenerated
	typeUnready
	typeInitiateGame
	typeRequestRando
	typeAnnounceCharmNotchCosts
	typeRequestCharmNotchCosts
	typeConfirmCharmNotchCostsReceived
	typeDatasSend
	typeDatasSendConfirm
	typeInitiateSyncGame
	typeApplySettings
	typeRequestSettings
	typeISReady
	typeDatasReceive
	typeDatasReceiveConfirm
	typeConnectedPlayersChanged
)

type Message interface {
	msgType() messageType
	appendTo([]byte) []byte
}

type ConnectMessage struct {
	ServerName string
}

func (m ConnectMessage) msgType() messageType {
	return typeConnect
}

func (m ConnectMessage) appendTo(b []byte) []byte {
	return appendString(b, m.ServerName)
}

type PingMessage struct {
	ReplyValue uint32
}

func (m PingMessage) msgType() messageType {
	return typePing
}

func (m PingMessage) appendTo(b []byte) []byte {
	return byteOrder.AppendUint32(b, m.ReplyValue)
}

type DisconnectMessage struct{}

func (m DisconnectMessage) msgType() messageType {
	return typeDisconnect
}

func (m DisconnectMessage) appendTo(b []byte) []byte { return b }

type JoinMessage struct {
	DisplayName string
	RandoID     int32
	PlayerID    int32
	Mode        byte
}

func (JoinMessage) msgType() messageType {
	return typeJoin
}

func (m JoinMessage) appendTo(b []byte) []byte {
	b = appendString(b, m.DisplayName)
	b = byteOrder.AppendUint32(b, uint32(m.RandoID))
	b = byteOrder.AppendUint32(b, uint32(m.PlayerID))
	b = append(b, m.Mode)
	return b
}

type ReadyMessage struct {
	Room          string
	Nickname      string
	Mode          byte
	ReadyMetadata []KeyValuePair
}

type KeyValuePair struct {
	Key   string `json:"Item1"`
	Value string `json:"Item2"`
}

func (m ReadyMessage) msgType() messageType {
	return typeReady
}

func (m ReadyMessage) appendTo(b []byte) []byte {
	b = appendString(b, m.Room)
	b = appendString(b, m.Nickname)
	b = append(b, m.Mode)
	b = appendJSON(b, m.ReadyMetadata)
	return b
}

type ReadyConfirmMessage struct {
	Ready int32
	Names []string
}

func (ReadyConfirmMessage) msgType() messageType {
	return typeReadyConfirm
}

func (m ReadyConfirmMessage) appendTo(b []byte) []byte {
	b = byteOrder.AppendUint32(b, toUint32(len(m.Names)))
	b = appendJSON(b, m.Names)
	return b
}

type ReadyDenyMessage struct {
	Description string
}

func (ReadyDenyMessage) msgType() messageType {
	return typeReadyDeny
}

func (m ReadyDenyMessage) appendTo(b []byte) []byte {
	return appendString(b, m.Description)
}

type UnreadyMessage struct{}

func (UnreadyMessage) msgType() messageType {
	return typeUnready
}

func (m UnreadyMessage) appendTo(b []byte) []byte {
	return b
}

type InitiateGameMessage struct {
	Options struct {
		RandomizationAlgorithm any
	}
}

func (InitiateGameMessage) msgType() messageType {
	return typeInitiateGame
}

func (m InitiateGameMessage) appendTo(b []byte) []byte {
	return appendJSON(b, m)
}

type RequestRandoMessage struct{}

func (RequestRandoMessage) msgType() messageType {
	return typeRequestRando
}

func (m RequestRandoMessage) appendTo(b []byte) []byte {
	return b
}

type RandoGeneratedMessage struct {
	Items map[string][]Placement
	Seed  int32
}

func (RandoGeneratedMessage) msgType() messageType {
	return typeRandoGenerated
}

func (m RandoGeneratedMessage) appendTo(b []byte) []byte {
	b = appendJSON(b, m.Items)
	b = byteOrder.AppendUint32(b, uint32(m.Seed))
	return b
}

type ResultMessage struct {
	PlayerID              int32
	RandoID               int32
	Nicknames             []string
	ReadyMetadata         [][]KeyValuePair
	ItemsSpoiler          SpoilerLogs
	Placements            map[string][]ResultPlacement
	PlayerItemsPlacements map[string]string
	GeneratedHash         string
}

// Some objects' fields must be named "Item1" and "Item2" in the output,
// because they were defined as tuples in C# and that is what C# calls
// its tuple fields, even when names are otherwise explicitly given to them.
// See https://learn.microsoft.com/en-us/dotnet/csharp/language-reference/builtin-types/value-tuples

type ResultPlacement struct {
	Item     string `json:"Item1"`
	Location string `json:"Item2"`
}

type SpoilerLogs struct {
	FullOrderedItemsLog     string
	IndividualWorldSpoilers map[string]string
}

func (ResultMessage) msgType() messageType {
	return typeResult
}

func (m ResultMessage) appendTo(b []byte) []byte {
	b = byteOrder.AppendUint32(b, uint32(m.PlayerID))
	b = byteOrder.AppendUint32(b, uint32(m.RandoID))
	b = appendJSON(b, m.Nicknames)
	b = appendJSON(b, m.ReadyMetadata)
	b = appendJSON(b, m.ItemsSpoiler)
	b = appendJSON(b, m.Placements)
	b = appendJSON(b, m.PlayerItemsPlacements)
	b = appendString(b, m.GeneratedHash)
	return b
}

type DataReceiveMessage struct {
	Label   string
	Content string
	From    string
	FromID  int32
}

func (DataReceiveMessage) msgType() messageType {
	return typeDataReceive
}

func (m DataReceiveMessage) appendTo(b []byte) []byte {
	b = appendString(b, m.Label)
	b = appendString(b, m.Content)
	b = appendString(b, m.From)
	b = byteOrder.AppendUint32(b, uint32(m.FromID))
	return b
}

type DataReceiveConfirmMessage struct {
	Label string
	Data  string
	From  string
}

func (DataReceiveConfirmMessage) msgType() messageType {
	return typeDataReceiveConfirm
}

func (m DataReceiveConfirmMessage) appendTo(b []byte) []byte {
	b = appendString(b, m.Label)
	b = appendString(b, m.Data)
	b = appendString(b, m.From)
	return b
}

const LabelMultiworldItem = "MultiWorld-Item"

type Placement struct {
	Item     string `json:"Item1"`
	Location string `json:"Item2"`
}

var byteOrder = binary.LittleEndian
