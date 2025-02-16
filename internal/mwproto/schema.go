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
}

type ConnectMessage struct {
	ServerName string
}

func (m ConnectMessage) msgType() messageType {
	return typeConnect
}

type PingMessage struct {
	ReplyValue uint32
}

func (m PingMessage) msgType() messageType {
	return typePing
}

type DisconnectMessage struct{}

func (m DisconnectMessage) msgType() messageType {
	return typeDisconnect
}

type JoinMessage struct {
	DisplayName string
	RandoID     int32
	PlayerID    int32
	Mode        byte
}

func (JoinMessage) msgType() messageType {
	return typeJoin
}

type JoinConfirmMessage struct {
}

func (JoinConfirmMessage) msgType() messageType {
	return typeJoinConfirm
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

type ReadyConfirmMessage struct {
	Ready int32
	Names []string
}

func (ReadyConfirmMessage) msgType() messageType {
	return typeReadyConfirm
}

type ReadyDenyMessage struct {
	Description string
}

func (ReadyDenyMessage) msgType() messageType {
	return typeReadyDeny
}

type UnreadyMessage struct{}

func (UnreadyMessage) msgType() messageType {
	return typeUnready
}

type InitiateGameMessage struct {
	Options struct {
		RandomizationAlgorithm any
	}
}

func (InitiateGameMessage) msgType() messageType {
	return typeInitiateGame
}

type RequestRandoMessage struct{}

func (RequestRandoMessage) msgType() messageType {
	return typeRequestRando
}

type RandoGeneratedMessage struct {
	Items map[string][]Placement
	Seed  int32
}

func (RandoGeneratedMessage) msgType() messageType {
	return typeRandoGenerated
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

type DataReceiveMessage struct {
	Label   string
	Content string
	From    string
	FromID  int32
}

func (DataReceiveMessage) msgType() messageType {
	return typeDataReceive
}

type DataReceiveConfirmMessage struct {
	Label string
	Data  string
	From  string
}

func (DataReceiveConfirmMessage) msgType() messageType {
	return typeDataReceiveConfirm
}

type SaveMessage struct{}

func (SaveMessage) msgType() messageType {
	return typeSave
}

type DataSendMessage struct {
	Label   string
	Content string
	To      int32
	TTL     int32
}

func (DataSendMessage) msgType() messageType {
	return typeDataSend
}

type DataSendConfirmMessage struct {
	Label   string
	Content string
	To      int32
}

func (DataSendConfirmMessage) msgType() messageType {
	return typeDataSendConfirm
}

type RequestCharmNotchCostsMessage struct{}

func (RequestCharmNotchCostsMessage) msgType() messageType {
	return typeRequestCharmNotchCosts
}

type AnnounceCharmNotchCostsMessage struct {
	PlayerID   int32
	NotchCosts map[int]int
}

func (AnnounceCharmNotchCostsMessage) msgType() messageType {
	return typeAnnounceCharmNotchCosts
}

type ConfirmCharmNotchCostsReceived struct {
	PlayerID int32
}

func (ConfirmCharmNotchCostsReceived) msgType() messageType {
	return typeConfirmCharmNotchCostsReceived
}

const LabelMultiworldItem = "MultiWorld-Item"

type Placement struct {
	Item     string `json:"Item1"`
	Location string `json:"Item2"`
}

var byteOrder = binary.LittleEndian
