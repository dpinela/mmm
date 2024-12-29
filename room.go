package main

import (
	"cmp"
	"log"
	"maps"
	"math/bits"
	"math/rand/v2"
	"slices"
	"sync"
	"time"
)

type room struct {
	name    string
	players map[uid]player
}

type player struct {
	nickname       string
	roomMessages   chan<- roomMessage
	generatedRando *world
}

type placement struct {
	Item     string
	Location string
}

type sphere []placement

type world struct {
	placements map[string][]sphere
	seed       int64
}

type roomCommand func(r *room)

func (r *room) run(commands <-chan roomCommand) {
	log.Printf("opened room %q", r.name)
	for cmd := range commands {
		cmd(r)
		if len(r.players) == 0 {
			log.Printf("closing room %q, no players left", r.name)
			return
		}
	}
}

const roomMessageTimeout = 5 * time.Second

func join(id uid, p player) roomCommand {
	return func(r *room) {
		r.players[id] = p
		r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
	}
}

func leave(id uid) roomCommand {
	return func(r *room) {
		if _, exists := r.players[id]; !exists {
			log.Printf("nonexistent player attempted to leave room %q", r.name)
			return
		}
		delete(r.players, id)
		r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
	}
}

func uploadRando(id uid, seed world) roomCommand {
	return func(r *room) {
		p, exists := r.players[id]
		if !exists {
			log.Printf("nonexistent player attempted to upload a rando in room %q", r.name)
			return
		}
		p.generatedRando = &seed
		r.players[id] = p

		for _, p := range r.players {
			if p.generatedRando == nil {
				return
			}
		}

		log.Printf("generating rando for room %q", r.name)

		worlds := make([]world, 0, len(r.players))
		for _, p := range r.players {
			worlds = append(worlds, *p.generatedRando)
		}
		slices.SortStableFunc(worlds, func(w1, w2 world) int {
			return cmp.Compare(w1.seed, w2.seed)
		})
		result := mix(worlds)
		log.Printf("generated rando for room %q:", r.name)
		for _, p := range result {
			log.Printf("[%d]%s @ [%d]%s", p.item.world, p.item.name, p.location.world, p.location.name)
		}
	}
}

func (r *room) nicknames() []string {
	nicknames := make([]string, 0, len(r.players))
	for _, p := range r.players {
		nicknames = append(nicknames, p.nickname)
	}
	return nicknames
}

func (r *room) broadcast(msg roomMessage) {
	var wg sync.WaitGroup
	wg.Add(len(r.players))

	timeoutCh := make(chan struct{})
	time.AfterFunc(roomMessageTimeout, func() { close(timeoutCh) })

	trySend := func(p player) {
		defer wg.Done()
		select {
		case p.roomMessages <- msg:
		case <-timeoutCh:
			log.Printf("broadcast to %s timed out; message was %v", p.nickname, msg)
		}
	}

	for _, p := range r.players {
		trySend(p)
	}

	wg.Wait()
}

func (r *room) startRandomization() {
	r.broadcast(randomizationStarting{})
}

type mixedPlacement struct {
	item     qualifiedItem
	location qualifiedLocation
}

type qualifiedName struct {
	world int
	name  string
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
	for i, w := range worlds {
		for g, spheres := range w.placements {
			groups[g] = append(groups[g], groupWorld{world: i, spheres: spheres})
		}
	}
	groupNames := slices.Sorted(maps.Keys(groups))

	var placements []mixedPlacement
	for _, g := range groupNames {
		placements = append(placements, mixGroup(rng, groups[g])...)
	}
	return placements
}

type groupWorld struct {
	world   int
	spheres []sphere
}

func mixGroup(rng *rand.Rand, worlds []groupWorld) []mixedPlacement {
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
			availableLocations = append(availableLocations, qualifiedLocation{world: i, name: p.Location})
			availableItems = append(availableItems, qualifiedItem{world: i, name: p.Item})
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
		placements = append(placements, mixedPlacement{item: item, location: loc})

		w := item.world
		ns := &nextSpheres[w]
		ns.itemsToUnlock--
		hasMoreSpheres := ns.index < len(worlds[w].spheres)
		if ns.itemsToUnlock == 0 && hasMoreSpheres {
			newSphere := worlds[w].spheres[ns.index]
			ns.index++
			ns.itemsToUnlock = len(newSphere)
			for _, p := range newSphere {
				availableLocations = append(availableLocations, qualifiedLocation{world: w, name: p.Location})
				availableItems = append(availableItems, qualifiedItem{world: w, name: p.Item})
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
