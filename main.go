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
	"github.com/dpinela/mmm/internal/storage"
)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	db, err := storage.Open(":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	srv, err := net.Listen("tcp", "localhost:38281")
	if err != nil {
		return err
	}
	defer srv.Close()
	s := &server{name: "MMM", db: db, rooms: map[string]chan<- roomCommand{}}
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

	db *storage.DB

	roomsMu sync.Mutex
	rooms   map[string]chan<- roomCommand
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

type randomizationResult mwproto.ResultMessage

func (randomizationResult) isRoomMessage() {}

func (srv *server) openRoom(roomName string) chan<- roomCommand {
	srv.roomsMu.Lock()
	defer srv.roomsMu.Unlock()

	rCh := srv.rooms[roomName]
	if rCh == nil {
		ch := make(chan roomCommand)
		srv.rooms[roomName] = ch
		rCh = ch
		r := &room{name: roomName, players: map[uid]player{}}
		go func() {
			r.run(ch)
			srv.closeRoom(roomName)
		}()
	}
	return rCh
}

func (srv *server) closeRoom(roomName string) {
	srv.roomsMu.Lock()
	defer srv.roomsMu.Unlock()

	delete(srv.rooms, roomName)
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
			roomCommands <- leave(conn.uid)
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

awaitReadyOrJoin:
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
			p := player{nickname: msg.Nickname, roomMessages: roomMessages}
			roomCommands <- join(conn.uid, p)
			break awaitReadyOrJoin
		case mwproto.JoinMessage:
			_, err := conn.server.db.PendingItems(int(msg.PlayerID), int(msg.RandoID))
			if err != nil {
				log.Printf("find pending items for player %d in rando %d: %v", msg.PlayerID, msg.RandoID, err)
			}
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
				roomCommands <- leave(conn.uid)
				roomCommands = nil
				roomMessages = nil
				goto awaitReadyOrJoin
			case mwproto.InitiateGameMessage:
				if !(msg.Options.RandomizationAlgorithm == 0.0 || msg.Options.RandomizationAlgorithm == "Default") {
					log.Printf("invalid randomization algorithm from %s: %v", conn.RemoteAddr(), msg.Options.RandomizationAlgorithm)
					continue
				}
				roomCommands <- (*room).startRandomization
			case mwproto.RandoGeneratedMessage:
				placementMap := make(map[string][]sphere, len(msg.Items))
				for group, placements := range msg.Items {
					spheres := make([]sphere, len(placements))
					for i, p := range placements {
						spheres[i] = sphere{placement(p)}
					}
					placementMap[group] = spheres
				}

				roomCommands <- uploadRando(conn.uid, world{
					seed:       int64(msg.Seed),
					placements: placementMap,
				})
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
					log.Printf("send rando request to %s: %v", conn.RemoteAddr(), err)
				}
			case randomizationResult:
				if err := mwproto.Write(conn, mwproto.ResultMessage(msg)); err != nil {
					log.Printf("send rando result to %s: %v", conn.RemoteAddr(), err)
				}
				roomCommands <- leave(conn.uid)
				roomCommands = nil
				// this may lead to timeouts in the room if other message
				// sends for us were queued up after the rando result was
				// calculated
				roomMessages = nil
				goto awaitReadyOrJoin
			}
		}
	}
}
