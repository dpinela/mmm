package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"slices"
	"sync"
	"time"

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

type room struct {
	name    string
	players []player
}

func (r *room) run(commands <-chan roomCommand) {
	log.Printf("opened room %q", r.name)
	for cmd := range commands {
		cmd(r)
		if len(r.players) == 0 {
			log.Printf("closing room %q, no players left", r.name)
			return
		}
	}
}

const roomMessageTimeout = 5 * time.Second

func (r *room) join(p player) {
	r.players = append(r.players, p)
	r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
}

func (r *room) leave(id uid) {
	for i, p := range r.players {
		if p.uid == id {
			r.players = slices.Delete(r.players, i, i+1)
			r.broadcast(playersJoinedMessage{nicknames: r.nicknames()})
			return
		}
	}
	log.Printf("nonexistent player attempted to leave room %q", r.name)
}

func (r *room) nicknames() []string {
	nicknames := make([]string, len(r.players))
	for i, p := range r.players {
		nicknames[i] = p.nickname
	}
	return nicknames
}

func (r *room) broadcast(msg roomMessage) {
	var wg sync.WaitGroup
	wg.Add(len(r.players))

	timeoutCh := make(chan struct{})
	time.AfterFunc(roomMessageTimeout, func() { close(timeoutCh) })

	trySend := func(p player) {
		defer wg.Done()
		select {
		case p.roomMessages <- msg:
		case <-timeoutCh:
			log.Printf("broadcast to %s timed out; message was %v", p.nickname, msg)
		}
	}

	for _, p := range r.players {
		trySend(p)
	}

	wg.Wait()
}

func (r *room) startRandomization() {
	r.broadcast(randomizationStarting{})
}

type roomCommand func(r *room)

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
