package mwproto

import (
	"errors"
	"io"
	"log"
	"net"
	"time"
)

type Client struct {
	conn      net.Conn
	realInbox chan Message
	inbox     chan Message
	outbox    chan Message
}

func Dial(serverAddr string) (*Client, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:      conn,
		realInbox: make(chan Message, chanBufferSize),
		inbox:     make(chan Message, chanBufferSize),
		outbox:    make(chan Message, chanBufferSize),
	}
	go c.recvMessages()
	go c.sendMessages()
	go c.ping()
	return c, nil
}

func (c *Client) Inbox() <-chan Message { return c.inbox }

func (c *Client) Send(m Message) { c.outbox <- m }

func (c *Client) Close() { close(c.outbox) }

// Will terminate when outbox is closed.
func (c *Client) sendMessages() {
	for m := range c.outbox {
		err := Write(c.conn, m)
		if err != nil {
			log.Println("error sending MW message:", err)
		}
	}
	_ = Write(c.conn, DisconnectMessage{})
	c.conn.Close()
}

// Will terminate when the network connection is closed from either side.
func (c *Client) recvMessages() {
	defer close(c.realInbox)
	for {
		msg, err := Read(c.conn)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if errors.Is(err, io.EOF) {
			log.Println("MW connection closed from server side")
			return
		}
		if err != nil {
			log.Println("error reading MW message:", err)
			continue
		}
		c.realInbox <- msg
	}
}

// Will terminate when either too many pings go unanswered, or the connection
// is closed.
func (c *Client) ping() {
	pingTimer := time.NewTicker(pingInterval)
	defer pingTimer.Stop()
	defer close(c.inbox)
	unansweredPings := 0
	for {
		select {
		case <-pingTimer.C:
			unansweredPings++
			if unansweredPings == reconnectThreshold {
				return
			}
			c.outbox <- PingMessage{}
		case msg, ok := <-c.realInbox:
			if !ok {
				return
			}
			if _, isPing := msg.(PingMessage); isPing {
				unansweredPings = 0
			} else {
				c.inbox <- msg
			}
		}
	}
}

const (
	chanBufferSize     = 100
	pingInterval       = 5 * time.Second
	reconnectThreshold = 5
)
