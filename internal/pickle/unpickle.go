package pickle

import (
	"fmt"
	"io"
)

type Symbol struct {
	Module string
	Attr   string
}

type Object struct {
	Class     any
	Arguments any
}

type Tuple struct {
	array any
}

func Decode(r io.Reader, p any) error {
	proto := make([]byte, 2)
	if _, err := io.ReadFull(r, proto); err != nil {
		return err
	}
	if proto[0] != opcodePROTO {
		return fmt.Errorf("unexpected opcode at start: %02x", proto[0])
	}
	if proto[1] < 4 {
		return fmt.Errorf("invalid pickle version: %d", proto[1])
	}
	vm := machine{
		src: readerFrom(r),
	}
	v, err := vm.exec()
	if err != nil {
		return err
	}
	*(p.(*any)) = v
	return nil
}
