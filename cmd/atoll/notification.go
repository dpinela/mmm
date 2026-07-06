package main

import (
	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/internal/ping"
)

type notifier struct {
	notchCostTopic    ping.Topic[indexfile.RandoID]
	shuffleTopic      ping.Topic[indexfile.RandoID]
	playerChangeTopic ping.Topic[indexfile.RandoID]
	itemTopic         ping.Topic[subscriberID]
}

func newNotifier() *notifier {
	return &notifier{}
}

type subscriberID struct {
	randoID  indexfile.RandoID
	playerID int64
}
