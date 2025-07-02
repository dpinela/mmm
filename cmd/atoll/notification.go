package main

import (
	"slices"
	"sync"
)

type notifier struct {
	mu              sync.Mutex
	itemSubscribers map[subscriberID][]chan<- struct{}
}

func newNotifier() *notifier {
	return &notifier{
		itemSubscribers: map[subscriberID][]chan<- struct{}{},
	}
}

type subscriberID struct {
	randoID  int64
	playerID int64
}

func (n *notifier) notifyNewItems(id subscriberID) {
	n.mu.Lock()
	// ensure the slice isn't modified out from under us after we unlock the mutex
	targets := slices.Clone(n.itemSubscribers[id])
	n.mu.Unlock()
	for _, t := range targets {
		select {
		case t <- struct{}{}:
		default:
			// If the channel buffer is full, the goroutine on the other side
			// hasn't processed the previous message, either because it exited
			// or because it hasn't gotten to it yet.
			// Either way, that makes this notification redundant, so drop it.
			continue
		}
	}
}

func (n *notifier) listenNewItems(id subscriberID, ch chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.itemSubscribers[id] = append(n.itemSubscribers[id], ch)
}

func (n *notifier) muteNewItems(id subscriberID, ch chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.itemSubscribers[id]
	i := slices.Index(n.itemSubscribers[id], ch)
	if i == -1 {
		panic("removed subscriber that was not listening")
	}
	s[i] = s[len(s)-1]
	n.itemSubscribers[id] = s[:len(s)-1]
}
