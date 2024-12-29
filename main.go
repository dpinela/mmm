package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/dpinela/mmm/internal/mwproto"
)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	srv, err := net.Listen("tcp", "localhost:38281")
	if err != nil {
		return err
	}
	defer srv.Close()
	s := &server{name: "MMM", rooms: map[string]chan<- roomCommand{}}
	var nextUID uid
	for {
		conn, err := srv.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		cc := &clientConn{server: s, uid: nextUID, Conn: conn}
		nextUID++
		go cc.serve()
	}
}

type server struct {
	name string

	roomsMu sync.Mutex
	rooms   map[string]chan<- roomCommand
}

type player struct {
	uid          uid
	nickname     string
	roomMessages chan<- roomMessage
}

type roomMessage interface {
	isRoomMessage()
}

type playersJoinedMessage struct {
	nicknames []string
}

func (playersJoinedMessage) isRoomMessage() {}

type randomizationStarting struct{}

func (randomizationStarting) isRoomMessage() {}

func (srv *server) openRoom(roomName string) chan<- roomCommand {
	srv.roomsMu.Lock()
	defer srv.roomsMu.Unlock()

	rCh := srv.rooms[roomName]
	if rCh == nil {
		ch := make(chan roomCommand)
		srv.rooms[roomName] = ch
		rCh = ch
		r := &room{name: roomName}
		go r.run(ch)
	}
	return rCh
}

type clientConn struct {
	server *server
	uid    uid
	net.Conn
}

type uid uint64

func (conn *clientConn) read(ch chan<- mwproto.Message) {
	for {
		msg, err := mwproto.Read(conn)
		if err != nil {
			log.Printf("read from %s: %v", conn.RemoteAddr(), err)
			var netErr net.Error
			if errors.As(err, &netErr) || errors.Is(err, io.EOF) {
				close(ch)
				return
			}
			continue
		}
		ch <- msg
	}
}

func (conn *clientConn) serve() {
	defer conn.Close()

	log.Printf("new connection from %s", conn.RemoteAddr())

	clientMessages := make(chan mwproto.Message)
	go conn.read(clientMessages)

	var roomMessages chan roomMessage
	var roomCommands chan<- roomCommand

	defer func() {
		if roomCommands != nil {
			roomCommands <- func(r *room) {
				r.leave(conn.uid)
			}
		}
	}()

	for {
		msg, ok := <-clientMessages
		if !ok {
			return
		}
		if _, ok := msg.(mwproto.ConnectMessage); ok {
			if err := mwproto.Write(conn, mwproto.ConnectMessage{ServerName: conn.server.name}); err != nil {
				log.Printf("acknowledge connection from %s: %v", conn.RemoteAddr(), err)
				return
			}
			break
		}
		log.Printf("unexpected message (awaiting connection) from %s: %v", conn.RemoteAddr(), msg)
	}

awaitReady:
	for {
		msg, ok := <-clientMessages
		if !ok {
			return
		}
		switch msg := msg.(type) {
		case mwproto.PingMessage:
			if err := mwproto.Write(conn, msg); err != nil {
				log.Printf("respond to ping from %s: %v", conn.RemoteAddr(), err)
				return
			}
			continue
		case mwproto.DisconnectMessage:
			log.Printf("connection from %s terminated", conn.RemoteAddr())
			return
		case mwproto.ReadyMessage:
			if msg.Mode != 0 {
				log.Printf("invalid room mode from %s: %d", conn.RemoteAddr(), msg.Mode)
				if err := mwproto.Write(conn, mwproto.ReadyDenyMessage{Description: "invalid room mode"}); err != nil {
					log.Printf("send ready deny to %s: %v", conn.RemoteAddr(), err)
					return
				}
			}
			roomMessages = make(chan roomMessage)
			roomCommands = conn.server.openRoom(msg.Room)
			p := player{nickname: msg.Nickname, uid: conn.uid, roomMessages: roomMessages}
			roomCommands <- func(r *room) {
				r.join(p)
			}
			break awaitReady
		default:
			log.Printf("unexpected message (awaiting ready) from %s: %v", conn.RemoteAddr(), msg)
		}
	}

	for {
		select {
		case msg, ok := <-clientMessages:
			if !ok {
				return
			}
			switch msg := msg.(type) {
			case mwproto.PingMessage:
				if err := mwproto.Write(conn, msg); err != nil {
					log.Printf("respond to ping from %s: %v", conn.RemoteAddr(), err)
					return
				}
				continue
			case mwproto.DisconnectMessage:
				log.Printf("connection from %s terminated", conn.RemoteAddr())
				return
			case mwproto.UnreadyMessage:
				// this can also deadlock if room is broadcasting a message
				// but hasn't sent it to this session yet
				roomCommands <- func(r *room) {
					r.leave(conn.uid)
				}
				roomCommands = nil
				roomMessages = nil
				goto awaitReady
			case mwproto.InitiateGameMessage:
				if msg.RandomizationAlgorithm != 0 {
					log.Printf("invalid randomization algorithm from %s: %v", conn.RemoteAddr(), msg.RandomizationAlgorithm)
					continue
				}
				roomCommands <- func(r *room) {
					r.startRandomization()
				}
			case mwproto.RandoGeneratedMessage:
				log.Printf("seed: %v", msg.Seed)
				for group, placements := range msg.Items {
					log.Printf("items of group %s:", group)
					for _, p := range placements {
						log.Printf("%s @ %s", p.Item, p.Location)
					}
				}
			default:
				log.Printf("unexpected message (in room) from %s: %v", conn.RemoteAddr(), msg)
			}
		case msg := <-roomMessages:
			switch msg := msg.(type) {
			case playersJoinedMessage:
				if err := mwproto.Write(conn, mwproto.ReadyConfirmMessage{Names: msg.nicknames}); err != nil {
					log.Printf("send nicknames to %s: %v", conn.RemoteAddr(), err)
					return
				}
			case randomizationStarting:
				if err := mwproto.Write(conn, mwproto.RequestRandoMessage{}); err != nil {
					log.Printf("sending rando request to %s: %v", conn.RemoteAddr(), err)
				}
			}
		}
	}
}
