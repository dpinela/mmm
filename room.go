package main

import (
	"log"
	"slices"
	"sync"
	"time"
)

type room struct {
	name    string
	players []player
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

func (r *room) join(p player) {
	r.players = append(r.players, p)
	r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
}

func (r *room) leave(id uid) {
	for i, p := range r.players {
		if p.uid == id {
			r.players = slices.Delete(r.players, i, i+1)
			r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
			return
		}
	}
	log.Printf("nonexistent player attempted to leave room %q", r.name)
}

func (r *room) nicknames() []string {
	nicknames := make([]string, len(r.players))
	for i, p := range r.players {
		nicknames[i] = p.nickname
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
