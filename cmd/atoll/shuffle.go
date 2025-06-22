package main

import (
	"maps"
	"math/bits"
	"math/rand/v2"
	"slices"
)

type world struct {
	playerID   int64
	placements map[string][]sphere
	seed       int64
}

type placement struct {
	Item     string
	Location string
}

type sphere []placement

type mixedPlacement struct {
	Item     qualifiedItem
	Location qualifiedLocation
	Group    string
}

type qualifiedName struct {
	World int64
	Name  string
}

type qualifiedLocation qualifiedName
type qualifiedItem qualifiedName

func mix(worlds []world) []mixedPlacement {
	var seed uint128
	for _, w := range worlds {
		seed = seed.mul(0xAAAA_AAAA_AAAA_AAAA).add(uint64(w.seed))
	}
	rng := rand.New(rand.NewPCG(seed.hi, seed.lo))

	groups := map[string][]groupWorld{}
	for _, w := range worlds {
		for g, spheres := range w.placements {
			groups[g] = append(groups[g], groupWorld{playerID: w.playerID, spheres: spheres})
		}
	}
	groupNames := slices.Sorted(maps.Keys(groups))

	var placements []mixedPlacement
	for _, g := range groupNames {
		placements = append(placements, mixGroup(rng, groups[g], g)...)
	}
	return placements
}

type groupWorld struct {
	playerID int64
	spheres  []sphere
}

func mixGroup(rng *rand.Rand, worlds []groupWorld, groupName string) []mixedPlacement {
	type upcomingSphere struct {
		index         int
		itemsToUnlock int
	}

	var (
		availableLocations []qualifiedLocation
		availableItems     []qualifiedItem
		nextSpheres        = make([]upcomingSphere, len(worlds))
	)
	for i, w := range worlds {
		if len(w.spheres) == 0 {
			continue
		}
		nextSpheres[i] = upcomingSphere{index: 1, itemsToUnlock: len(w.spheres[0])}
		for _, p := range w.spheres[0] {
			availableLocations = append(availableLocations, qualifiedLocation{World: w.playerID, Name: p.Location})
			availableItems = append(availableItems, qualifiedItem{World: w.playerID, Name: p.Item})
		}
	}

	var placements []mixedPlacement

	for len(availableLocations) > 0 {
		var (
			loc  qualifiedLocation
			item qualifiedItem
		)
		loc, availableLocations = sample(rng, availableLocations)
		item, availableItems = sample(rng, availableItems)
		placements = append(placements, mixedPlacement{Item: item, Location: loc, Group: groupName})

		w := slices.IndexFunc(worlds, func(gw groupWorld) bool {
			return gw.playerID == item.World
		})
		if w == -1 {
			panic("item placed for world not passed to mixGroup???")
		}
		ns := &nextSpheres[w]
		ns.itemsToUnlock--
		hasMoreSpheres := ns.index < len(worlds[w].spheres)
		if ns.itemsToUnlock == 0 && hasMoreSpheres {
			newSphere := worlds[w].spheres[ns.index]
			ns.index++
			ns.itemsToUnlock = len(newSphere)
			for _, p := range newSphere {
				availableLocations = append(availableLocations, qualifiedLocation{World: item.World, Name: p.Location})
				availableItems = append(availableItems, qualifiedItem{World: item.World, Name: p.Item})
			}
		}
	}

	return placements
}

func sample[S ~[]T, T any](rng *rand.Rand, items S) (pick T, rest S) {
	i := rng.IntN(len(items))
	pick = items[i]
	items[i] = items[len(items)-1]
	rest = items[:len(items)-1]
	return
}

type uint128 struct {
	hi, lo uint64
}

func (x uint128) mul(k uint64) uint128 {
	var xk uint128
	xk.hi, xk.lo = bits.Mul64(x.lo, k)
	xk.hi += x.hi * k
	return xk
}

func (x uint128) add(k uint64) uint128 {
	var y uint128
	var c uint64
	y.lo, c = bits.Add64(x.lo, k, 0)
	y.hi = x.hi + c
	return y
}
