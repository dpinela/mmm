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
	case typeReadyConfirm:
		return unmarshalReadyConfirm(payload)
	case typeJoin:
		return unmarshalJoin(payload)
	case typeUnready:
		return UnreadyMessage{}, nil
	case typeInitiateGame:
		return unmarshalInitiateGame(payload)
	case typeRandoGenerated:
		return unmarshalRandoGenerated(payload)
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

func unmarshalReadyConfirm(payload []byte) (m ReadyConfirmMessage, err error) {
	// skip the Ready field
	if len(payload) < 4 {
		err = fmt.Errorf("payload too short: need 4 bytes, got %d", len(payload))
		return
	}
	_, err = unmarshalJSON(payload[4:], &m.Names)
	return
}

func unmarshalJoin(payload []byte) (m JoinMessage, err error) {
	m.DisplayName, payload, err = unmarshalString(payload)
	if err != nil {
		return
	}
	if len(payload) < 9 {
		err = fmt.Errorf("remaining payload too short: need 5 bytes, got %d", len(payload))
		return
	}
	m.RandoID = int32(byteOrder.Uint32(payload[:4]))
	m.PlayerID = int32(byteOrder.Uint32(payload[4:8]))
	m.Mode = payload[8]
	return
}

func unmarshalInitiateGame(payload []byte) (m InitiateGameMessage, err error) {
	_, err = unmarshalJSON(payload, &m)
	return
}

func unmarshalRandoGenerated(payload []byte) (m RandoGeneratedMessage, err error) {
	payload, err = unmarshalJSON(payload, &m.Items)
	if err != nil {
		return
	}
	m.Seed = int32(byteOrder.Uint32(payload[:4]))
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

func unmarshalJSON[T any](payload []byte, dest *T) (remainder []byte, err error) {
	raw, remainder, err := unmarshalBytes(payload)
	if err != nil {
		return
	}
	err = json.Unmarshal(raw, dest)
	return
}
