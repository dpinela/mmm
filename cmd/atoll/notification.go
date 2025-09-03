package main

import (
	"github.com/dpinela/mmm/internal/ping"
)

type notifier struct {
	notchCostTopic    ping.Topic[int64]
	shuffleTopic      ping.Topic[int64]
	playerChangeTopic ping.Topic[int64]
	itemTopic         ping.Topic[subscriberID]
}

func newNotifier() *notifier {
	return &notifier{}
}

type subscriberID struct {
	randoID  int64
	playerID int64
}
