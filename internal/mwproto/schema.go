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

var byteOrder = binary.LittleEndian
