package mwproto

import (
	"fmt"
	"io"
)

func Write(w io.Writer, msg Message) error {
	encoded := make([]byte, headerSize)
	encoded = msg.appendTo(encoded)
	byteOrder.PutUint32(encoded[:4], toUint32(len(encoded)))
	byteOrder.PutUint32(encoded[4:8], uint32(msg.msgType()))
	_, err := w.Write(encoded)
	return err
}

func appendString(out []byte, s string) []byte {
	out = byteOrder.AppendUint32(out, toUint32(len(s)))
	out = append(out, s...)
	return out
}

func toUint32(x int) uint32 {
	v := uint32(x)
	if int64(v) != int64(x) {
		panic(fmt.Sprintf("value out of uint32 range: %d", x))
	}
	return v
}
