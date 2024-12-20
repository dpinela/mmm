package mwproto

import (
	"encoding/binary"
)

type Type uint32

const (
	TypeInvalid    Type = iota
	TypeSharedCore      // unused
	TypeConnect

	NumTypes
)

type Message interface {
	msgType() Type
	appendTo([]byte) []byte
}

type ConnectMessage struct {
	ServerName string
}

func (m ConnectMessage) msgType() Type {
	return TypeConnect
}

func (m ConnectMessage) appendTo(b []byte) []byte {
	return appendString(b, m.ServerName)
}

var byteOrder = binary.LittleEndian
