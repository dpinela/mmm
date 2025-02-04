package main

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/dpinela/mmm/internal/approto"
)

type apDataStorage map[string]any

func (s apDataStorage) apply(msg approto.SetMessage) (oldValue, newValue any, err error) {
	if strings.HasPrefix(msg.Key, approto.ReadOnlyKeyPrefix) {
		err = fmt.Errorf("cannot modify read-only key %q", msg.Key)
		return
	}
	origVal, ok := s[msg.Key]
	if !ok {
		origVal = msg.Default
	}
	var newVal any
	for _, op := range msg.Operations {
		switch op.Operation {
		case "replace":
			newVal = op.Value
		case "default":
			if ok {
				newVal = origVal
			} else {
				newVal = msg.Default
			}
		case "add":
			switch origVal := origVal.(type) {
			case float64:
				v, matches := op.Value.(float64)
				if !matches {
					err = fmt.Errorf("add: %v and %v not of the same type", origVal, op.Value)
					return
				}
				newVal = origVal + v
			case []any:
				v, matches := op.Value.([]any)
				if !matches {
					err = fmt.Errorf("add: %v and %v not of the same type", origVal, op.Value)
					return
				}
				newVal = append(origVal, v...)
			default:
				err = fmt.Errorf("add: invalid operand: %v", origVal)
			}
		case "mul":
			newVal, err = mathOp("mul", origVal, op.Value, func(a, b float64) float64 { return a * b })
			if err != nil {
				return
			}
		case "pow":
			newVal, err = mathOp("pow", origVal, op.Value, math.Pow)
			if err != nil {
				return
			}
		case "mod":
			newVal, err = mathOp("mul", origVal, op.Value, math.Mod)
			if err != nil {
				return
			}
		case "floor":
			newVal, err = mathOp("floor", origVal, op.Value, func(a, _ float64) float64 { return math.Floor(a) })
			if err != nil {
				return
			}
		case "ceil":
			newVal, err = mathOp("ceil", origVal, op.Value, func(a, _ float64) float64 { return math.Ceil(a) })
			if err != nil {
				return
			}
		case "max":
			newVal, err = mathOp("max", origVal, op.Value, math.Max)
			if err != nil {
				return
			}
		case "min":
			newVal, err = mathOp("min", origVal, op.Value, math.Min)
			if err != nil {
				return
			}
		case "and":
			newVal, err = mathOp("and", origVal, op.Value, func(a, b float64) float64 { return float64(int64(a) & int64(b)) })
			if err != nil {
				return
			}
		case "or":
			newVal, err = mathOp("or", origVal, op.Value, func(a, b float64) float64 { return float64(int64(a) | int64(b)) })
			if err != nil {
				return
			}
		case "xor":
			newVal, err = mathOp("and", origVal, op.Value, func(a, b float64) float64 { return float64(int64(a) ^ int64(b)) })
			if err != nil {
				return
			}
		case "left_shift":
			newVal, err = mathOp("and", origVal, op.Value, func(a, b float64) float64 { return float64(int64(a) << int64(b)) })
			if err != nil {
				return
			}
		case "right_shift":
			newVal, err = mathOp("and", origVal, op.Value, func(a, b float64) float64 { return float64(int64(a) >> int64(b)) })
			if err != nil {
				return
			}
		case "remove":
			w, matches := origVal.([]any)
			if !matches {
				err = fmt.Errorf("remove: %v is not a list", origVal)
				return
			}
			switch op.Value.(type) {
			case float64, string:
				i := slices.Index(w, op.Value)
				if i != -1 {
					newVal = slices.Delete(w, i, i+1)
				} else {
					newVal = origVal
				}
			default:
				// The original implementation technically permits this, but it does nothing
				// useful so no-one should ever do it.
				err = fmt.Errorf("remove: %v is not comparable", op.Value)
				return
			}
		case "pop":
			switch w := origVal.(type) {
			case []any:
				v, matches := op.Value.(float64)
				if !matches {
					err = fmt.Errorf("pop: index %v is not a number", op.Value)
					return
				}
				if i := int(v); i >= 0 && i < len(w) {
					newVal = slices.Delete(w, i, i+1)
				} else {
					newVal = origVal
				}
			case map[string]any:
				switch v := op.Value.(type) {
				case string:
					// modifies the original map in-place, but that's fine
					delete(w, v)
					newVal = w
				default:
					err = fmt.Errorf("remove: %v is not comparable", op.Value)
					return
				}
			default:
				err = fmt.Errorf("remove: %v is not a dictionary or list", origVal)
				return
			}
		case "update":
			w, origMatches := origVal.(map[string]any)
			d, matches := op.Value.(map[string]any)
			if !(origMatches && matches) {
				err = fmt.Errorf("update: %v and %v are not both dictionaries", origVal, op.Value)
				return
			}
			for k, v := range d {
				w[k] = v
			}
			newVal = w
		default:
			err = fmt.Errorf("unknown data storage op: %q", op.Operation)
			return
		}
		s[msg.Key] = newVal
	}
	return origVal, newVal, nil
}

func mathOp(name string, a, b any, op func(a, b float64) float64) (float64, error) {
	w, origMatches := a.(float64)
	v, matches := b.(float64)
	if !(origMatches && matches) {
		return 0, fmt.Errorf("%s: %v and %v are not both numbers", name, a, b)
	}
	return op(w, v), nil
}

func nonMatchingArgsErr(a, b any) error {
	return fmt.Errorf("add: %v and %v not of the same type", a, b)
}
