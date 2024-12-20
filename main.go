package main

import (
	"errors"
	"fmt"
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
	for {
		conn, err := srv.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go serveClient(conn)
	}
}

func serveClient(conn net.Conn) {
	log.Printf("connection from %s", conn.RemoteAddr())
	for {
		msg, err := mwproto.Read(conn)
		if err != nil {
			log.Printf("read from %s: %v", conn.RemoteAddr(), err)
			var netErr net.Error
			if errors.As(err, &netErr) {
				return
			}
			continue
		}
		_, ok := msg.(mwproto.ConnectMessage)
		if ok {
			err := mwproto.Write(conn, mwproto.ConnectMessage{ServerName: "MMM"})
			if err != nil {
				log.Printf("write to %s: %v", conn.RemoteAddr(), err)
				return
			}
			break
		}
		log.Printf("conn from %s: unexpected message: %v", conn.RemoteAddr(), msg)
	}
	for {
	}
}
