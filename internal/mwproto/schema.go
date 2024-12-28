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

type ReadyMessage struct {
	Room          string
	Nickname      string
	Mode          byte
	ReadyMetadata [][]string
}

func (m ReadyMessage) msgType() messageType {
	return typeReady
}

func (m ReadyMessage) appendTo(b []byte) []byte {
	panic("Ready message not meant to be sent from server")
}

type ReadyConfirmMessage struct {
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
	RandomizationAlgorithm int
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

var byteOrder = binary.LittleEndian
