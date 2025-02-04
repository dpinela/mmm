package mwproto

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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
		return unmarshal[ConnectMessage](payload)
	case typeDisconnect:
		return unmarshal[DisconnectMessage](payload)
	case typePing:
		return unmarshal[PingMessage](payload)
	case typeReady:
		return unmarshal[ReadyMessage](payload)
	case typeReadyConfirm:
		return unmarshal[ReadyConfirmMessage](payload)
	case typeJoin:
		return unmarshal[JoinMessage](payload)
	case typeUnready:
		return UnreadyMessage{}, nil
	case typeInitiateGame:
		return unmarshal[InitiateGameMessage](payload)
	case typeRequestRando:
		return RequestRandoMessage{}, nil
	case typeRandoGenerated:
		return unmarshal[RandoGeneratedMessage](payload)
	case typeResult:
		return unmarshal[ResultMessage](payload)
	case typeDataReceive:
		return unmarshal[DataReceiveMessage](payload)
	default:
		return nil, fmt.Errorf("read message: unknown message type: %d", msgType)
	}
}

func unmarshal[T Message](payload []byte) (T, error) {
	var msg T
	err := unmarshalInto(payload, reflect.ValueOf(&msg).Elem())
	return msg, err
}

func unmarshalInto(payload []byte, v reflect.Value) error {
	for i := range v.NumField() {
		field := v.Field(i)
		switch field.Kind() {
		case reflect.Uint8:
			if len(payload) == 0 {
				return io.ErrUnexpectedEOF
			}
			field.SetUint(uint64(payload[0]))
			payload = payload[1:]
		case reflect.Int32:
			if len(payload) < 4 {
				return io.ErrUnexpectedEOF
			}
			field.SetInt(int64(int32(byteOrder.Uint32(payload[:4]))))
			payload = payload[4:]
		case reflect.Uint32:
			if len(payload) < 4 {
				return io.ErrUnexpectedEOF
			}
			field.SetUint(uint64(byteOrder.Uint32(payload[:4])))
			payload = payload[4:]
		case reflect.String:
			s, rest, err := unmarshalString(payload)
			if err != nil {
				return err
			}
			payload = rest
			field.SetString(s)
		default:
			rest, err := unmarshalJSON(payload, field.Addr().Interface())
			if err != nil {
				return err
			}
			payload = rest
		}
	}
	return nil
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

func unmarshalJSON(payload []byte, dest any) (remainder []byte, err error) {
	raw, remainder, err := unmarshalBytes(payload)
	if err != nil {
		return
	}
	err = json.Unmarshal(raw, dest)
	return
}
