package main

import (
	"slices"
	"sync"
)

type notifier struct {
	roomMu          sync.Mutex
	roomSubscribers map[int64][]chan<- struct{}
	itemMu          sync.Mutex
	itemSubscribers map[subscriberID][]chan<- struct{}
}

func newNotifier() *notifier {
	return &notifier{
		itemSubscribers: map[subscriberID][]chan<- struct{}{},
		roomSubscribers: map[int64][]chan<- struct{}{},
	}
}

type subscriberID struct {
	randoID  int64
	playerID int64
}

func (n *notifier) notifyShuffleDone(roomID int64) {
	n.roomMu.Lock()
	// ensure the slice isn't modified out from under us after we unlock the mutex
	targets := slices.Clone(n.roomSubscribers[roomID])
	defer n.roomMu.Unlock()

	for _, t := range targets {
		select {
		case t <- struct{}{}:
		default:
			continue
		}
	}
}

func (n *notifier) listenShuffleDone(roomID int64, ch chan struct{}) {
	n.roomMu.Lock()
	defer n.roomMu.Unlock()
	n.roomSubscribers[roomID] = append(n.roomSubscribers[roomID], ch)
}

func (n *notifier) muteShuffleDone(roomID int64, ch chan struct{}) {
	n.roomMu.Lock()
	defer n.roomMu.Unlock()
	n.roomSubscribers[roomID] = removeSubscriber(n.roomSubscribers[roomID], ch)
}

func (n *notifier) notifyNewItems(id subscriberID) {
	n.itemMu.Lock()
	// ensure the slice isn't modified out from under us after we unlock the mutex
	targets := slices.Clone(n.itemSubscribers[id])
	n.itemMu.Unlock()
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
	n.itemMu.Lock()
	defer n.itemMu.Unlock()
	n.itemSubscribers[id] = append(n.itemSubscribers[id], ch)
}

func (n *notifier) muteNewItems(id subscriberID, ch chan struct{}) {
	n.itemMu.Lock()
	defer n.itemMu.Unlock()
	n.itemSubscribers[id] = removeSubscriber(n.itemSubscribers[id], ch)
}

func removeSubscriber(s []chan<- struct{}, ch chan struct{}) []chan<- struct{} {
	i := slices.Index(s, ch)
	if i == -1 {
		panic("removed subscriber that was not listening")
	}
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}
