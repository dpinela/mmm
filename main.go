package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"

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
	s := &server{name: "MMM", rooms: map[string]*room{}}
	for {
		conn, err := srv.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		cc := &clientConn{server: s, Conn: conn}
		go cc.serve()
	}
}

type server struct {
	name  string
	rooms map[string]*room
}

type room struct {
	players map[string]net.Conn
}

type clientConn struct {
	server *server
	net.Conn
}

func (conn *clientConn) serve() {
	defer conn.Close()

	handle := awaitConnection

	for {
		msg, err := mwproto.Read(conn)
		if err != nil {
			var netErr net.Error
			log.Printf("read from %s: %v", conn.RemoteAddr(), err)
			if errors.As(err, &netErr) || errors.Is(err, io.EOF) {
				return
			}
			continue
		}

		switch msg.(type) {
		case mwproto.PingMessage:
			if err := mwproto.Write(conn, msg); err != nil {
				log.Printf("respond to ping from %s: %v", conn.RemoteAddr(), err)
				return
			}
			continue
		case mwproto.DisconnectMessage:
			log.Printf("connection from %s terminated", conn.RemoteAddr())
			return
		}

		newHandle, err := handle(conn, msg)
		if err != nil {
			log.Printf("handle message from %s: %v", conn.RemoteAddr(), err)
			var netErr net.Error
			if errors.As(err, &netErr) {
				return
			}
		}
		handle = newHandle
	}
}

type messageHandler func(conn *clientConn, msg mwproto.Message) (messageHandler, error)

func awaitConnection(conn *clientConn, msg mwproto.Message) (messageHandler, error) {
	if _, ok := msg.(mwproto.ConnectMessage); !ok {
		log.Printf("unexpected message (awaiting connection) from %s: %v", conn.RemoteAddr(), msg)
		return awaitConnection, nil
	}
	if err := mwproto.Write(conn, mwproto.ConnectMessage{ServerName: conn.server.name}); err != nil {
		return nil, err
	}
	return ignoreEverything, nil
}

func ignoreEverything(conn *clientConn, msg mwproto.Message) (messageHandler, error) {
	return ignoreEverything, nil
}
