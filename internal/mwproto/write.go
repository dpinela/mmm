package mwproto

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

func Write(w io.Writer, msg Message) error {
	encoded := make([]byte, headerSize)
	encoded = appendMessage(encoded, msg)
	byteOrder.PutUint32(encoded[:4], toUint32(len(encoded)))
	byteOrder.PutUint32(encoded[4:8], uint32(msg.msgType()))
	_, err := w.Write(encoded)
	return err
}

func appendMessage(b []byte, m Message) []byte {
	v := reflect.ValueOf(m)
	for i := range v.NumField() {
		field := v.Field(i)
		switch field.Kind() {
		case reflect.Uint8:
			b = append(b, byte(field.Uint()))
		case reflect.Int32:
			b = byteOrder.AppendUint32(b, uint32(field.Int()))
		case reflect.Uint32:
			b = byteOrder.AppendUint32(b, uint32(field.Uint()))
		case reflect.String:
			b = appendString(b, field.String())
		default:
			b = appendJSON(b, field.Interface())
		}
	}
	return b
}

func appendString(out []byte, s string) []byte {
	out = appendVarint32(out, len(s))
	out = append(out, s...)
	return out
}

func appendJSON(out []byte, value any) []byte {
	s, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	out = appendVarint32(out, len(s))
	out = append(out, s...)
	return out
}

// Used for writing the length of strings.
// See https://learn.microsoft.com/en-us/dotnet/api/system.io.binarywriter.write7bitencodedint for the origin of this format.
func appendVarint32(out []byte, x int) []byte {
	v := int32(x)
	if int64(v) != int64(x) {
		panic(fmt.Sprintf("value out of int32 range: %d", x))
	}
	u := uint32(v)
	for {
		b := byte(u & (1<<7 - 1))
		u >>= 7
		if u == 0 {
			return append(out, b)
		}
		out = append(out, b|1<<7)
	}
}

func toUint32(x int) uint32 {
	v := uint32(x)
	if int64(v) != int64(x) {
		panic(fmt.Sprintf("value out of uint32 range: %d", x))
	}
	return v
}
