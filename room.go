package main

import (
	"log"
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
	generatedRando *randoSeed
}

type placement struct {
	Item     string
	Location string
}

type sphere []placement

type randoSeed struct {
	placements map[string][]sphere
	seed       int
}

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

func uploadRando(id uid, seed randoSeed) roomCommand {
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

type roomCommand func(r *room)
