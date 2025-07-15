package main

import (
	"github.com/dpinela/mmm/internal/ping"
)

type notifier struct {
	shuffleTopic ping.Topic[int64]
	itemTopic    ping.Topic[subscriberID]
}

func newNotifier() *notifier {
	return &notifier{}
}

type subscriberID struct {
	randoID  int64
	playerID int64
}
