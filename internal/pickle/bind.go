package pickle

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type StringMap map[string]any

func bind(pyobj any, dest reflect.Value, requireStringKeys bool) error {
	switch dest.Kind() {
	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := pyobj.(int64)
		if !ok {
			return fmt.Errorf("expected int, got %T", pyobj)
		}
		dest.SetInt(n)
	case reflect.String:
		s, ok := pyobj.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", pyobj)
		}
		dest.SetString(s)
	case reflect.Bool:
		b, ok := pyobj.(bool)
		if !ok {
			return fmt.Errorf("expected bool, got %T", pyobj)
		}
		dest.SetBool(b)
	case reflect.Slice:
		switch pyobj := pyobj.(type) {
		case *[]any:
			s := reflect.MakeSlice(dest.Type(), len(*pyobj), len(*pyobj))
			for i, v := range *pyobj {
				if err := bind(v, s.Index(i), requireStringKeys); err != nil {
					return wrapError(err, strconv.Itoa(i))
				}
			}
			dest.Set(s)
		case map[any]struct{}:
			s := reflect.MakeSlice(dest.Type(), len(pyobj), len(pyobj))
			i := 0
			for v := range pyobj {
				if err := bind(v, s.Index(i), requireStringKeys); err != nil {
					return wrapError(err, strconv.Itoa(i))
				}
				i++
			}
			dest.Set(s)
		case Tuple:
			t := reflect.ValueOf(pyobj.array)
			n := t.Len()
			s := reflect.MakeSlice(dest.Type(), n, n)
			for i := range n {
				if err := bind(t.Index(i).Interface(), s.Index(i), requireStringKeys); err != nil {
					return wrapError(err, strconv.Itoa(i))
				}
			}
			dest.Set(s)
		default:
			return fmt.Errorf("expected list, set or tuple, got %T", pyobj)
		}
	case reflect.Struct:
		switch pyobj := pyobj.(type) {
		case map[any]any:
			t := dest.Type()
			for i := range t.NumField() {
				f := t.Field(i)
				tags := strings.Split(f.Tag.Get(tagKey), ",")
				reqSK := slices.Contains(tags, requireStringKeysTag)
				if slices.Contains(tags, remainderTag) {
					if err := bind(pyobj, dest.Field(i), reqSK); err != nil {
						return err
					}
				} else {
					v, ok := pyobj[f.Name]
					if !ok {
						v, ok = pyobj[snakeCase(f.Name)]
					}
					if !ok {
						return fmt.Errorf("key %s not found", f.Name)
					}
					if err := bind(v, dest.Field(i), reqSK); err != nil {
						return wrapError(err, f.Name)
					}
				}
				continue
			}
		case Object:
			t := dest.Type()
			args := reflect.ValueOf(pyobj.Arguments.array)
			nArgs := args.Len()
			nFields := dest.NumField()
			if nArgs != nFields {
				return fmt.Errorf("expected %d arguments, got %d", nFields, nArgs)
			}
			for i := range nArgs {
				reqSK := t.Field(i).Tag.Get(tagKey) == requireStringKeysTag
				// Remainder tag not supported here since it makes no sense; the set
				// of expected fields for an object is closed anyway.
				if err := bind(args.Index(i).Interface(), dest.Field(i), reqSK); err != nil {
					return wrapError(err, dest.Type().Field(i).Name)
				}
			}
		default:
			return fmt.Errorf("expected object or dict, got %T", pyobj)
		}
	case reflect.Map:
		dict, ok := pyobj.(map[any]any)
		if !ok {
			return fmt.Errorf("expected dict, got %T", pyobj)
		}
		m := reflect.MakeMapWithSize(dest.Type(), len(dict))
		mapType := dest.Type()
		keyBuf := reflect.New(mapType.Key()).Elem()
		valueBuf := reflect.New(mapType.Elem()).Elem()
		for k, v := range dict {
			if err := bind(k, keyBuf, requireStringKeys); err != nil {
				return err
			}
			if err := bind(v, valueBuf, requireStringKeys); err != nil {
				return err
			}
			m.SetMapIndex(keyBuf, valueBuf)
		}
		dest.Set(m)
	case reflect.Interface:
		if requireStringKeys {
			pyobj = restrictMapKeysToStrings(pyobj)
		}
		dest.Set(reflect.ValueOf(pyobj))
	default:
		panic("invalid target type: " + dest.Type().Name())
	}
	return nil
}

const (
	tagKey               = "pickle"
	requireStringKeysTag = "require_string_keys"
	remainderTag         = "remainder"
)

var ucPattern = regexp.MustCompile(`\p{Lu}+`)

func snakeCase(name string) string {
	s := ucPattern.ReplaceAllStringFunc(name, func(x string) string {
		return "_" + strings.ToLower(x)
	})
	// We expect the original name to be an exported struct field,
	// so it will always start with an uppercase letter.
	if strings.HasPrefix(s, "_") {
		s = s[1:]
	}
	return s
}

func restrictMapKeysToStrings(x any) any {
	switch x := x.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, v := range x {
			s, ok := k.(string)
			if !ok {
				continue
			}
			m[s] = restrictMapKeysToStrings(v)
		}
		return m
	case *[]any:
		for i, v := range *x {
			(*x)[i] = restrictMapKeysToStrings(v)
		}
		return x
	default:
		return x
	}
}

func wrapError(err error, key string) error {
	path := []string{key}
	pe, ok := err.(pathError)
	if !ok {
		return pathError{path, err}
	}
	path = append(path, pe.path...)
	return pathError{path, pe.elemError}
}

type pathError struct {
	path      []string
	elemError error
}

func (err pathError) Error() string {
	return strings.Join(err.path, ".") + ": " + err.elemError.Error()
}
