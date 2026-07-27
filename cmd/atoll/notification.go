package main

import (
	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/internal/ping"
)

type notifier struct {
	notchCostTopic    ping.Topic[indexfile.MWRandoID]
	shuffleTopic      ping.Topic[indexfile.MWRandoID]
	playerChangeTopic ping.Topic[indexfile.MWRandoID]
	itemTopic         ping.Topic[subscriberID]
}

func newNotifier() *notifier {
	return &notifier{}
}

type subscriberID struct {
	randoID  indexfile.MWRandoID
	playerID int64
}
