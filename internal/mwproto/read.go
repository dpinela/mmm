package mwproto

import (
	"encoding/json"
	"fmt"
	"io"
)

const headerSize = 24

func Read(r io.Reader) (Message, error) {
	const lengthFieldSize = 4
	const minMessageSize = headerSize
	const maxMessageSize = 1 << 24

	lengthBuf := make([]byte, lengthFieldSize)
	if _, err := io.ReadFull(r, lengthBuf); err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}
	length := byteOrder.Uint32(lengthBuf)
	if length < minMessageSize || length > maxMessageSize {
		// Skip any remaining bytes in the message.
		_, _ = io.CopyN(io.Discard, r, int64(length)-int64(len(lengthBuf)))
		return nil, fmt.Errorf("read message: length out of bounds: got %d, want at least %d and at most %d", length, minMessageSize, maxMessageSize)
	}

	msgBuf := make([]byte, length-lengthFieldSize)
	if _, err := io.ReadFull(r, msgBuf); err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}
	msgType := messageType(byteOrder.Uint32(msgBuf[:4]))
	payload := msgBuf[headerSize-lengthFieldSize:]
	// SenderUID and MessageID can be ignored.
	switch msgType {
	case typeConnect:
		return ConnectMessage{}, nil
	case typeDisconnect:
		return DisconnectMessage{}, nil
	case typePing:
		return unmarshalPing(payload), nil
	case typeReady:
		return unmarshalReady(payload)
	case typeUnready:
		return UnreadyMessage{}, nil
	default:
		return nil, fmt.Errorf("read message: unknown message type: %d", msgType)
	}
}

func unmarshalPing(payload []byte) PingMessage {
	return PingMessage{byteOrder.Uint32(payload)}
}

func unmarshalReady(payload []byte) (m ReadyMessage, err error) {
	m.Room, payload, err = unmarshalString(payload)
	if err != nil {
		return
	}
	m.Nickname, payload, err = unmarshalString(payload)
	if err != nil {
		return
	}
	m.Mode = payload[0]
	rawMetadata, payload, err := unmarshalBytes(payload[1:])
	err = json.Unmarshal(rawMetadata, &m.ReadyMetadata)
	return
}

func unmarshalString(payload []byte) (str string, remainder []byte, err error) {
	b, r, err := unmarshalBytes(payload)
	return string(b), r, err
}

func unmarshalBytes(payload []byte) (str []byte, remainder []byte, err error) {
	length := 0
	for i, b := range payload {
		length |= int(b&0x7f) << (i * 7)
		if b&0x80 != 0 {
			continue
		}
		start := i + 1
		end := start + length
		if end > len(payload) {
			return nil, payload, fmt.Errorf("string value length exceeds message payload; got %d, remaining payload is %d bytes long", length, len(payload))
		}
		return payload[start:end], payload[end:], nil
	}
	return nil, payload, fmt.Errorf("unterminated string value length: % 02x", payload)
}
