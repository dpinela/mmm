package pickle

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

const (
	opcodeMARK             = 0x28
	opcodeEMPTY_TUPLE      = 0x29
	opcodeSTOP             = 0x2e
	opcodeBININT           = 0x4a
	opcodeBININT1          = 0x4b
	opcodeBININT2          = 0x4d
	opcodeNONE             = 0x4e
	opcodeBINUNICODE       = 0x58
	opcodeREDUCE           = 0x52
	opcodeEMPTY_LIST       = 0x5d
	opcodeAPPEND           = 0x61
	opcodeAPPENDS          = 0x65
	opcodeBINGET           = 0x68
	opcodeLONG_BINGET      = 0x6a
	opcodeSETITEM          = 0x73
	opcodeTUPLE            = 0x74
	opcodeSETITEMS         = 0x75
	opcodeEMPTY_DICT       = 0x7d
	opcodePROTO            = 0x80
	opcodeNEWOBJ           = 0x81
	opcodeTUPLE1           = 0x85
	opcodeTUPLE2           = 0x86
	opcodeTUPLE3           = 0x87
	opcodeNEWTRUE          = 0x88
	opcodeNEWFALSE         = 0x89
	opcodeSHORT_BINUNICODE = 0x8c
	opcodeBINUNICODE8      = 0x8d
	opcodeEMPTY_SET        = 0x8f
	opcodeADDITEMS         = 0x90
	opcodeSTACK_GLOBAL     = 0x93
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

func (vm *machine) exec() (any, error) {
	for {
		opcode, err := vm.src.ReadByte()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case opcodeMARK:
			vm.mark()
		case opcodeEMPTY_DICT:
			vm.emptyDict()
		case opcodeEMPTY_LIST:
			vm.emptyList()
		case opcodeEMPTY_TUPLE:
			vm.emptyTuple()
		case opcodeEMPTY_SET:
			vm.emptySet()
		case opcodeNONE:
			vm.none()
		case opcodeNEWTRUE:
			vm.bool(true)
		case opcodeNEWFALSE:
			vm.bool(false)
		case opcodeTUPLE:
			err = vm.tuple()
		case opcodeTUPLE1:
			err = vm.tuple1()
		case opcodeTUPLE2:
			err = vm.tuple2()
		case opcodeTUPLE3:
			err = vm.tuple3()
		case opcodeMEMOIZE:
			err = vm.memoize()
		case opcodeBINGET:
			err = vm.binGet()
		case opcodeLONG_BINGET:
			err = vm.longBinGet()
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
		case opcodeSTACK_GLOBAL:
			err = vm.stackGlobal()
		case opcodeAPPEND:
			err = vm.append()
		case opcodeAPPENDS:
			err = vm.appends()
		case opcodeADDITEMS:
			err = vm.addItems()
		case opcodeSETITEM:
			err = vm.setItem()
		case opcodeSETITEMS:
			err = vm.setItems()
		case opcodeREDUCE, opcodeNEWOBJ:
			// These two can be treated as equivalent for our purposes, since we
			// never instantiate any actual Python objects.
			err = vm.newObj()
		case opcodeSTOP:
			return vm.stop()
		default:
			return nil, fmt.Errorf("invalid opcode: %02x", opcode)
		}
		if err != nil {
			return nil, fmt.Errorf("execute opcode %02x: %w", opcode, err)
		}
	}
}

func (vm *machine) frame() error {
	// We don't actually use this instruction for anything, so ignore the argument.
	_, err := io.CopyN(io.Discard, vm.src, 8)
	return err
}

var errEmptyStack = errors.New("stack underflow")
var errStringTooLong = errors.New("string too long")
var errUnexpectedMarkArg = errors.New("unexpected mark as argument")
var errUnpairedKey = errors.New("unpaired dictionary key")

func (vm *machine) memoize() error {
	if len(vm.stack) == 0 {
		return errEmptyStack
	}
	vm.memo = append(vm.memo, vm.stack[len(vm.stack)-1])
	return nil
}

func (vm *machine) binGet() error {
	b, err := vm.src.ReadByte()
	if err != nil {
		return err
	}
	return vm.pushMemo(int64(b))
}

func (vm *machine) longBinGet() error {
	i, err := vm.readUint32()
	if err != nil {
		return err
	}
	return vm.pushMemo(int64(i))
}

func (vm *machine) pushMemo(i int64) error {
	if i >= int64(len(vm.memo)) {
		return fmt.Errorf("memo index %d out of bounds (memo size was %d)", i, len(vm.memo))
	}
	vm.stack = append(vm.stack, vm.memo[i])
	return nil
}

func (vm *machine) stackGlobal() error {
	v, err := vm.pop()
	if err != nil {
		return err
	}
	attr, ok := v.(string)
	if !ok {
		return fmt.Errorf("attribute name is %T, expected string", v)
	}
	v, err = vm.pop()
	if err != nil {
		return err
	}
	module, ok := v.(string)
	if !ok {
		return fmt.Errorf("module name is %T, expected string", v)
	}
	vm.stack = append(vm.stack, Symbol{Module: module, Attr: attr})
	return nil
}

func (vm *machine) emptyList()  { vm.stack = append(vm.stack, new([]any)) }
func (vm *machine) emptyDict()  { vm.stack = append(vm.stack, map[any]any{}) }
func (vm *machine) emptyTuple() { vm.stack = append(vm.stack, [0]any{}) }
func (vm *machine) emptySet()   { vm.stack = append(vm.stack, map[any]struct{}{}) }
func (vm *machine) none()       { vm.stack = append(vm.stack, nil) }
func (vm *machine) bool(b bool) { vm.stack = append(vm.stack, b) }
func (vm *machine) mark()       { vm.stack = append(vm.stack, mark{}) }

func (vm *machine) tuple() error {
	items, err := vm.popUntilMark()
	if err != nil {
		return err
	}
	tuple := reflect.New(reflect.ArrayOf(len(items), reflect.TypeFor[any]()))
	s := tuple.Elem().Slice(0, len(items)).Interface().([]any)
	copy(s, items)
	t := Tuple{array: tuple.Elem().Interface()}
	vm.stack = append(vm.stack, t)
	return nil
}

func (vm *machine) tuple1() error {
	v1, err := vm.pop()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, Tuple{[1]any{v1}})
	return nil
}

func (vm *machine) tuple2() error {
	v2, err := vm.pop()
	if err != nil {
		return err
	}
	v1, err := vm.pop()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, Tuple{[2]any{v1, v2}})
	return nil
}

func (vm *machine) tuple3() error {
	v3, err := vm.pop()
	if err != nil {
		return err
	}
	v2, err := vm.pop()
	if err != nil {
		return err
	}
	v1, err := vm.pop()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, Tuple{[3]any{v1, v2, v3}})
	return nil
}

func (vm *machine) append() error {
	item, err := vm.pop()
	if err != nil {
		return err
	}
	target, err := vm.peek()
	if err != nil {
		return err
	}
	list, ok := target.(*[]any)
	if !ok {
		return fmt.Errorf("target for APPEND is %T, wanted a list", target)
	}
	*list = append(*list, item)
	return nil
}

func (vm *machine) appends() error {
	items, err := vm.popUntilMark()
	if err != nil {
		return err
	}
	target, err := vm.peek()
	if err != nil {
		return err
	}
	list, ok := target.(*[]any)
	if !ok {
		return fmt.Errorf("target for APPENDS is %T, wanted a list", target)
	}
	*list = append(*list, items...)
	return nil
}

func (vm *machine) addItems() error {
	items, err := vm.popUntilMark()
	if err != nil {
		return err
	}
	target, err := vm.peek()
	if err != nil {
		return err
	}
	set, ok := target.(map[any]struct{})
	if !ok {
		return fmt.Errorf("target for SETITEM is %T, wanted a set", target)
	}
	for _, item := range items {
		set[item] = struct{}{}
	}
	return nil
}

func (vm *machine) setItem() error {
	value, err := vm.pop()
	if err != nil {
		return err
	}
	key, err := vm.pop()
	if err != nil {
		return err
	}
	if err := checkDictKey(key); err != nil {
		return err
	}
	target, err := vm.peek()
	if err != nil {
		return err
	}
	dict, ok := target.(map[any]any)
	if !ok {
		return fmt.Errorf("target for SETITEM is %T, wanted a dict", target)
	}
	dict[key] = value
	return nil
}

func (vm *machine) setItems() error {
	kvps, err := vm.popUntilMark()
	if err != nil {
		return err
	}
	if len(kvps)%2 != 0 {
		return errUnpairedKey
	}
	target, err := vm.peek()
	if err != nil {
		return err
	}
	dict, ok := target.(map[any]any)
	if !ok {
		return fmt.Errorf("target for SETITEMS is %T, wanted a dict", target)
	}
	for i := 0; i < len(kvps); i += 2 {
		if err := checkDictKey(kvps[i]); err != nil {
			return err
		}
		dict[kvps[i]] = kvps[i+1]
	}
	return nil
}

func checkDictKey(v any) error {
	switch v.(type) {
	case int64:
		return nil
	case string:
		return nil
	case bool:
		return nil
	case Tuple:
		return nil
	default:
		return fmt.Errorf("dict key has invalid type %T", v)
	}
}

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

func (vm *machine) newObj() error {
	v, err := vm.pop()
	if err != nil {
		return err
	}
	args, ok := v.(Tuple)
	if !ok {
		return fmt.Errorf("arguments are of type %T, expected Tuple", v)
	}
	cls, err := vm.pop()
	if err != nil {
		return err
	}
	vm.stack = append(vm.stack, Object{Class: cls, Arguments: args})
	return nil
}

func (vm *machine) stop() (any, error) {
	v, err := vm.pop()
	if err != nil {
		return nil, err
	}
	if len(vm.stack) > 0 {
		return nil, fmt.Errorf("%d values left in stack after STOP", len(vm.stack))
	}
	return v, nil
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

func (vm *machine) pop() (any, error) {
	if len(vm.stack) == 0 {
		return nil, errEmptyStack
	}
	v := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	if _, isMark := v.(mark); isMark {
		return nil, errUnexpectedMarkArg
	}
	return v, nil
}

func (vm *machine) popUntilMark() ([]any, error) {
	for i, v := range slices.Backward(vm.stack) {
		if _, isMark := v.(mark); isMark {
			values := vm.stack[i+1:]
			vm.stack = vm.stack[:i]
			return values, nil
		}
	}
	return nil, errEmptyStack
}

func (vm *machine) peek() (any, error) {
	if len(vm.stack) == 0 {
		return nil, errEmptyStack
	}
	return vm.stack[len(vm.stack)-1], nil
}
