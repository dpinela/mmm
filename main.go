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
		go serveClient(s, conn)
	}
}

type server struct {
	name  string
	rooms map[string]*room
}

type room struct {
	players map[string]net.Conn
}

func serveClient(srv *server, conn net.Conn) {
	defer conn.Close()

	log.Printf("connection from %s", conn.RemoteAddr())
	err := pumpMessages(conn, func(msg mwproto.Message) error {
		_, ok := msg.(mwproto.ConnectMessage)
		if ok {
			err := mwproto.Write(conn, mwproto.ConnectMessage{ServerName: srv.name})
			if err != nil {
				return err
			}
			return errPumpDone
		}
		return nil
	})
	if err != nil {
		log.Printf("connection from %s terminated: %v", conn.RemoteAddr(), err)
		return
	}
	err = pumpMessages(conn, func(_ mwproto.Message) error {
		return nil
	})
	if err != nil {
		log.Printf("connection from %s terminated: %v", conn.RemoteAddr(), err)
		return
	}
}

var errPumpDone = errors.New("done pumping messages")
var errDisconnected = errors.New("client disconnected")

func pumpMessages(conn net.Conn, handle func(msg mwproto.Message) error) error {
	for {
		msg, err := mwproto.Read(conn)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) || errors.Is(err, io.EOF) {
				return err
			}
			log.Printf("read from %s: %v", conn.RemoteAddr(), err)
			continue
		}

		switch msg.(type) {
		case mwproto.PingMessage:
			if err := mwproto.Write(conn, msg); err != nil {
				return err
			}
		case mwproto.DisconnectMessage:
			return errDisconnected
		}

		if err := handle(msg); err != nil {
			if err == errPumpDone {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) {
				return err
			}
			log.Printf("handle message from %s: %v", conn.RemoteAddr(), err)
		}
	}
}
