package ping

import (
	"slices"
	"sync"
)

// A Topic allows different goroutines to notify each other when they make state changes
// requiring timely action, like sending an item, or shuffling worlds in a room.
// Only a notification that some event occurred is sent; the receiving goroutine must
// look up the details itself from wherever the sender wrote them.
//
// A Topic is safe for concurrent use, and must not be copied. The zero Topic is a usable
// instance with no subscribers.
type Topic[Key comparable] struct {
	mu          sync.RWMutex
	subscribers map[Key][]chan<- struct{}
}

func (t *Topic[Key]) Listen(k Key, ch chan<- struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.subscribers == nil {
		t.subscribers = map[Key][]chan<- struct{}{}
	}
	t.subscribers[k] = append(t.subscribers[k], ch)
}

func (t *Topic[Key]) Mute(k Key, ch chan<- struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.subscribers[k]
	i := slices.Index(s, ch)
	if i == -1 {
		panic("removed subscriber that was not listening")
	}
	s[i] = s[len(s)-1]
	t.subscribers[k] = s[:len(s)-1]
}

func (t *Topic[Key]) Notify(k Key) {
	t.mu.RLock()
	// ensure the slice isn't modified out from under us after we unlock the mutex
	subscribers := slices.Clone(t.subscribers[k])
	t.mu.RUnlock()
	for _, t := range subscribers {
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
