package approto

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type ServerMessage interface {
	isServerMessage()
}

type ClientMessage interface {
	isClientMessage()
}

type Server struct {
	numConnections atomic.Int32
	connections    chan *ClientConn
	httpServer     http.Server
}

func Serve(port int) *Server {
	listener := &Server{
		connections: make(chan *ClientConn, 1),
	}
	listener.httpServer.Addr = fmt.Sprintf("localhost:%d", port)
	listener.httpServer.Handler = http.HandlerFunc(listener.handleConnection)

	go func() {
		log.Println("Starting up AP server")
		err := listener.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Println("error serving AP:", err)
			return
		}
	}()
	return listener
}

func (ls *Server) Accept() *ClientConn {
	return <-ls.connections
}

func (ls *Server) Close() error {
	return ls.httpServer.Close()
}

func (ls *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	apconn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer apconn.CloseNow()
	n := ls.numConnections.Load()
	if !(n == 0 && ls.numConnections.CompareAndSwap(n, n+1)) {
		log.Println("AP client rejected; only one allowed at a time")
		return
	}
	defer ls.numConnections.Add(-1)
	log.Println("AP client connected")
	// signal disconnection
	cconn := &ClientConn{
		inbox:  make(chan ClientMessage, 1),
		outbox: make(chan ServerMessage, 1),
	}
	defer func() { cconn.inbox <- nil }()
	ls.connections <- cconn
	ctx := r.Context()

	go func() {
		for {
			select {
			case msg, ok := <-cconn.outbox:
				if !ok {
					apconn.CloseNow()
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
			var cerr websocket.CloseError
			if errors.As(err, &cerr) {
				log.Println("AP client disconnected, code:", cerr.Code, "reason:", cerr.Reason)
				return
			}
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
			case "SetNotify":
				cmsg, err = tryParse[SetNotifyMessage](msg)
			case "LocationScouts":
				cmsg, err = tryParse[LocationScoutsMessage](msg)
			case "LocationChecks":
				cmsg, err = tryParse[LocationChecksMessage](msg)
			case "Sync":
				cmsg = SyncMessage{}
			case "Say":
				cmsg, err = tryParse[SayMessage](msg)
			default:
				log.Println("unknown client message:", unknownMessage.Cmd)
				continue
			}
			if err != nil {
				log.Printf("error parsing %s: %v", unknownMessage.Cmd, err)
				continue
			}
			cconn.inbox <- cmsg
		}
	}
}

type ClientConn struct {
	inbox  chan ClientMessage
	outbox chan ServerMessage
}

func (cc *ClientConn) Inbox() <-chan ClientMessage { return cc.inbox }
func (cc *ClientConn) Send(msg ServerMessage)      { cc.outbox <- msg }
func (cc *ClientConn) Close()                      { close(cc.outbox) }

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
