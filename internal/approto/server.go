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

type ServerMessage interface {
	isServerMessage()
}

type ClientMessage interface {
	isClientMessage()
}

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
			log.Println("AP client rejected; only one allowed at a time")
			return
		}
		log.Println("AP client connected")
		// signal disconnection
		defer func() { inboxCh <- nil }()
		ctx := r.Context()

		ri := roomInfo
		ri.Time = float64(time.Now().UnixMilli()) / float64(time.Millisecond)
		if err := wsjson.Write(ctx, apconn, []ServerMessage{ri}); err != nil {
			log.Println("error writing RoomInfo:", err)
			return
		}

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
				var (
					cmsg ClientMessage
					err  error
				)
				switch unknownMessage.Cmd {
				case "Connect":
					cmsg, err = tryParse[Connect](msg)
				case "GetDataPackage":
					cmsg, err = tryParse[GetDataPackage](msg)
				case "Set":
					cmsg, err = tryParse[SetMessage](msg)
				case "Get":
					cmsg, err = parseGet(msg)
				case "LocationScouts":
					cmsg, err = tryParse[LocationScoutsMessage](msg)
				case "LocationChecks":
					cmsg, err = tryParse[LocationChecksMessage](msg)
				default:
					log.Println("unknown client message:", unknownMessage.Cmd)
					continue
				}
				if err != nil {
					log.Printf("error parsing %s: %v", unknownMessage.Cmd, err)
					continue
				}
				inboxCh <- cmsg
			}
		}
	})
	go func() {
		log.Println("Starting up AP server")
		err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), h)
		if err != nil {
			log.Println("error serving AP:", err)
			return
		}
	}()
	return inboxCh, outboxCh
}

func tryParse[T ClientMessage](msg json.RawMessage) (ClientMessage, error) {
	var parsedMsg T
	if err := json.Unmarshal(msg, &parsedMsg); err != nil {
		return nil, fmt.Errorf("error parsing %T: %v", parsedMsg, err)
	}
	return parsedMsg, nil
}

func parseGet(msg json.RawMessage) (ClientMessage, error) {
	// encoding/json doesn't offer a way of putting excess keys into a map field;
	// this is a simple workaround.
	var (
		names struct {
			Keys []string
		}
		args map[string]any
	)
	if err := json.Unmarshal(msg, &args); err != nil {
		return nil, fmt.Errorf("error parsing Get: %v", err)
	}
	if err := json.Unmarshal(msg, &names); err != nil {
		return nil, fmt.Errorf("error parsing Get: %v", err)
	}
	delete(args, "keys")
	delete(args, "cmd")
	return GetMessage{Keys: names.Keys, Rest: args}, nil
}

type packet []json.RawMessage
