package approto

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func Serve(port int, roomInfo RoomInfo) (inbox <-chan ClientMessage, outbox chan<- ServerMessage) {
	var numConnections atomic.Int32
	inboxCh := make(chan ClientMessage, 10)
	outboxCh := make(chan ServerMessage, 10)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apconn, err := websocket.Accept(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}
		defer apconn.CloseNow()
		n := numConnections.Load()
		if !(n == 0 && numConnections.CompareAndSwap(n, n+1)) {
			return
		}
		// signal disconnection
		defer func() { inboxCh <- nil }()
		ctx := r.Context()
		go func() {
			for {
				select {
				case msg, ok := <-outboxCh:
					if !ok {
						return
					}
					if err := wsjson.Write(ctx, apconn, []ServerMessage{msg}); err != nil {
						log.Println("error writing AP message:", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		ri := roomInfo
		ri.Time = float64(time.Now().UnixMilli()) / float64(time.Millisecond)
		outboxCh <- ri

		var (
			buf            packet
			unknownMessage struct{ Cmd string }
		)
		for {
			if err := wsjson.Read(ctx, apconn, &buf); err != nil {
				log.Println("error reading AP packet:", err)
				return
			}
			for _, msg := range buf {
				if err := json.Unmarshal(msg, &unknownMessage); err != nil {
					log.Println("error parsing AP command:", err)
					continue
				}
				log.Println("unknown client message:", unknownMessage.Cmd)
			}
		}
	})
	go func() {
		err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), h)
		if err != nil {
			log.Println("error serving AP:", err)
			return
		}
	}()
	return inboxCh, outboxCh
}

type packet []json.RawMessage

type ServerMessage interface {
	isServerMessage()
}

type ClientMessage interface {
	isClientMessage()
}
