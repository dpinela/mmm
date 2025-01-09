package pickle

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	opcodeMARK             = 0x28
	opcodeBININT           = 0x4a
	opcodeBININT1          = 0x4b
	opcodeBININT2          = 0x4d
	opcodeBINUNICODE       = 0x58
	opcodeEMPTY_DICT       = 0x7d
	opcodePROTO            = 0x80
	opcodeSHORT_BINUNICODE = 0x8c
	opcodeBINUNICODE8      = 0x8d
	opcodeMEMOIZE          = 0x94
	opcodeFRAME            = 0x95
)

type machine struct {
	src    reader
	stack  []any
	memo   []any
	argBuf [8]byte
}

type mark struct{}

type reader interface {
	io.Reader
	io.ByteReader
}

func readerFrom(orig io.Reader) reader {
	if r, ok := orig.(reader); ok {
		return r
	}
	return bufio.NewReader(orig)
}

func (vm *machine) exec() error {
	for {
		opcode, err := vm.src.ReadByte()
		if err != nil {
			return err
		}
		switch opcode {
		case opcodeMARK:
			vm.mark()
		case opcodeEMPTY_DICT:
			vm.emptyDict()
		case opcodeMEMOIZE:
			err = vm.memoize()
		case opcodeFRAME:
			err = vm.frame()
		case opcodeSHORT_BINUNICODE:
			err = vm.shortBinunicode()
		case opcodeBINUNICODE:
			err = vm.binunicode()
		case opcodeBINUNICODE8:
			err = vm.binunicode8()
		case opcodeBININT:
			err = vm.binint()
		case opcodeBININT1:
			err = vm.binint1()
		case opcodeBININT2:
			err = vm.binint2()
		default:
			return fmt.Errorf("invalid opcode: %02x", opcode)
		}
		if err != nil {
			return fmt.Errorf("execute opcode %02x: %w", opcode, err)
		}
	}
}

func (vm *machine) frame() error {
	// We don't actually use this instruction for anything, so ignore the argument.
	_, err := io.CopyN(io.Discard, vm.src, 8)
	return err
}

var errEmptyStack = errors.New("empty stack")
var errStringTooLong = errors.New("string too long")

func (vm *machine) memoize() error {
	if len(vm.stack) == 0 {
		return errEmptyStack
	}
	vm.memo = append(vm.memo, vm.stack[len(vm.stack)-1])
	return nil
}

func (vm *machine) emptyDict() { vm.stack = append(vm.stack, map[any]any{}) }
func (vm *machine) mark()      { vm.stack = append(vm.stack, mark{}) }

func (vm *machine) shortBinunicode() error {
	size, err := vm.src.ReadByte()
	if err != nil {
		return err
	}
	return vm.pushString(uint64(size))
}

func (vm *machine) binunicode() error {
	n, err := vm.readUint32()
	if err != nil {
		return err
	}
	return vm.pushString(uint64(n))
}

func (vm *machine) binunicode8() error {
	n, err := vm.readUint64()
	if err != nil {
		return err
	}
	return vm.pushString(n)
}

func (vm *machine) binint() error {
	n, err := vm.readUint32()
	if err != nil {
		return err
	}
	v := int32(n)
	vm.stack = append(vm.stack, int64(v))
	return nil
}

func (vm *machine) binint1() error {
	n, err := vm.src.ReadByte()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, int64(n))
	return nil
}

func (vm *machine) binint2() error {
	n, err := vm.readUint16()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, int64(n))
	return nil
}

func (vm *machine) readUint16() (uint16, error) {
	if _, err := io.ReadFull(vm.src, vm.argBuf[:2]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(vm.argBuf[:2]), nil
}

func (vm *machine) readUint32() (uint32, error) {
	if _, err := io.ReadFull(vm.src, vm.argBuf[:4]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(vm.argBuf[:4]), nil
}

func (vm *machine) readUint64() (uint64, error) {
	if _, err := io.ReadFull(vm.src, vm.argBuf[:8]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(vm.argBuf[:8]), nil
}

func (vm *machine) pushString(n uint64) error {
	var s strings.Builder
	if n >= (1 << 63) {
		// won't fit in an int64
		return errStringTooLong
	}
	s.Grow(int(n))
	if _, err := io.CopyN(&s, vm.src, int64(n)); err != nil {
		return err
	}
	vm.stack = append(vm.stack, s.String())
	return nil
}
