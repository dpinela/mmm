package mwproto

import (
	"strconv"
	"strings"
)

func ParseQualifiedName(name string) (pid int, item string, ok bool) {
	const prefix = "MW("

	if !strings.HasPrefix(name, prefix) {
		return
	}
	qualifier, item, ok := strings.Cut(name[len(prefix):], ")_")
	if !ok {
		return
	}
	n, err := strconv.ParseInt(qualifier, 10, 32)
	if err != nil {
		ok = false
		return
	}
	return int(n), item, ok
}

func ParseDiscriminator(name string) (discriminator int64, ok bool) {
	name, ok = strings.CutSuffix(name, ")")
	if !ok {
		return
	}
	i := strings.LastIndex(name, "_(")
	if i == -1 {
		return
	}
	n, err := strconv.ParseInt(name[i+2:], 10, 64)
	if err != nil {
		return
	}
	return n, true
}

func StripDiscriminator(name string) string {
	name, _ = strings.CutSuffix(name, ")")
	i := strings.LastIndex(name, "_(")
	if i == -1 {
		return name
	}
	return name[:i]
}
