package main

import (
	"errors"
	"fmt"

	"github.com/dpinela/mmm/internal/mwproto"
)

func apToMWPlacements(data apdata) ([]mwproto.Placement, error) {
	slotID := singularKey(data.SlotInfo)
	slot := data.SlotInfo[slotID]
	dpkg, ok := data.Datapackage[slot.Game]
	if !ok {
		return nil, fmt.Errorf(".archipelago does not contain datapackage for main game %s", slot.Game)
	}
	itemNames, err := invert(dpkg.ItemNameToID, "duplicate item ID in datapackage")
	if err != nil {
		return nil, err
	}
	locationNames, err := invert(dpkg.LocationNameToID, "duplicate location ID in datapackage")
	if err != nil {
		return nil, err
	}
	placements, ok := data.Locations[slotID]
	if !ok {
		return nil, errors.New(".archipelago does not contain location data for its single slot")
	}
	var mwPlacements []mwproto.Placement
	for _, s := range data.Spheres {
		for _, loc := range s[slotID] {
			locName, ok := locationNames[loc]
			if !ok {
				locName = "Mystery_Place"
			}
			locName = fmt.Sprintf("%s_(%d)", locName, loc)
			p, ok := placements[loc]
			if !ok {
				fmt.Println("\tNOTHING @", locName)
				continue
			}
			if len(p) < 2 {
				fmt.Println("\tMISSING DATA @", locName)
				continue
			}
			itemName, ok := itemNames[p[0]]
			if !ok {
				return nil, fmt.Errorf("item missing from datapackage: %d", p[0])
			}
			// Ensure that item names as presented to the MW server are unique,
			// as required by the protocol.
			// The AP server implementation will strip the discriminator and
			// look up the real item ID from the data package anyway.
			itemName = fmt.Sprintf("%s_(%d)", itemName, len(mwPlacements))

			mwPlacements = append(mwPlacements, mwproto.Placement{
				Item:     itemName,
				Location: locName,
			})
		}
	}
	return mwPlacements, nil
}

func invert[K, V comparable](m map[K]V, errmsg string) (map[V]K, error) {
	w := make(map[V]K, len(m))
	for k, v := range m {
		if _, isDup := w[v]; isDup {
			return nil, fmt.Errorf("%s: %v", errmsg, v)
		}
		w[v] = k
	}
	return w, nil
}
