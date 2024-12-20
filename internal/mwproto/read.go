package mwproto

import (
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
	msgType := Type(byteOrder.Uint32(msgBuf[:4]))
	// SenderUID and MessageID can be ignored.
	switch msgType {
	case TypeConnect:
		return ConnectMessage{}, nil
	default:
		return nil, fmt.Errorf("read message: unknown message type: %d", msgType)
	}
}
