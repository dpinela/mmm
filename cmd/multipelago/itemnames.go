package main

import (
	"strings"

	"github.com/dpinela/mmm/internal/mwproto"
)

func prettifyItemNames(placements []mwproto.ResultPlacement, selfID int32) map[string]string {
	nameMapping := map[string]string{}
	uniqueBaseNames := map[string][]string{}
	for _, p := range placements {
		pid, item, ok := mwproto.ParseQualifiedName(p.Item)
		if !ok || pid == int(selfID) {
			continue
		}
		base := mwproto.StripDiscriminator(item)
		uniqueBaseNames[base] = append(uniqueBaseNames[base], item)
	}
	for base, names := range uniqueBaseNames {
		if len(names) == 1 {
			pretty := strings.ReplaceAll(base, "_", " ")
			nameMapping[pretty] = names[0]
			nameMapping[names[0]] = pretty
		} else {
			for _, name := range names {
				pretty := strings.ReplaceAll(name, "_", " ")
				nameMapping[pretty] = name
				nameMapping[name] = pretty
			}
		}
	}
	return nameMapping
}
