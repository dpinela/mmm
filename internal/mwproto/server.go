package mwproto

import (
	"errors"
	"io"
	"log"
	"net"
)

type Server struct {
	listener net.Listener
}

type ServerConn struct {
	conn   net.Conn
	inbox  chan Message
	outbox chan Message
}

func Listen(addr string) (*Server, error) {
	socket, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{socket}, nil
}

func (s *Server) Accept() (*ServerConn, error) {
	conn, err := s.listener.Accept()
	if err != nil {
		return nil, err
	}
	sconn := &ServerConn{
		conn:   conn,
		inbox:  make(chan Message, chanBufferSize),
		outbox: make(chan Message, chanBufferSize),
	}
	go sconn.recvMessages()
	go sconn.sendMessages()
	return sconn, nil
}

func (s *Server) Close() error { return s.listener.Close() }

func (sc *ServerConn) RemoteAddr() net.Addr  { return sc.conn.RemoteAddr() }
func (sc *ServerConn) Send(msg Message)      { sc.outbox <- msg }
func (sc *ServerConn) Inbox() <-chan Message { return sc.inbox }
func (sc *ServerConn) Close()                { close(sc.outbox) }

func (sc *ServerConn) recvMessages() {
	defer close(sc.inbox)
	for {
		msg, err := Read(sc.conn)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if errors.Is(err, io.EOF) {
			log.Println("MW connection closed from client side")
			return
		}
		if err != nil {
			log.Println("read MW message:", err)
			continue
		}
		if _, isPing := msg.(PingMessage); isPing {
			sc.outbox <- msg
		} else {
			sc.inbox <- msg
		}
	}
}

func (sc *ServerConn) sendMessages() {
	defer sc.conn.Close()
	for msg := range sc.outbox {
		err := Write(sc.conn, msg)
		if err != nil {
			log.Println("send MW message:", err)
		}
	}
}
