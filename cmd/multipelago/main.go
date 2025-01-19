package main

import (
	"bufio"
	"compress/zlib"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/pickle"
)

func main() {
	var opts options
	flag.StringVar(&opts.apfile, "apfile", "./AP.archipelago", "The Archipelago seed to serve")
	flag.StringVar(&opts.mwserver, "mwserver", "127.0.0.1:38281", "The multiworld server to join")
	flag.StringVar(&opts.mwroom, "mwroom", "", "The room to join")
	flag.Parse()

	if err := serve(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	apfile   string
	mwserver string
	mwroom   string
}

func serve(opts options) error {
	data, err := readAPFile(opts.apfile)
	if err != nil {
		return err
	}
	if len(data.ConnectNames) != 1 {
		return fmt.Errorf(".archipelago contains %d worlds, expected only one", len(data.ConnectNames))
	}
	err = joinServer(opts.mwserver, opts.mwroom, singularKey(data.ConnectNames))
	return nil
}

func singularKey(m map[string][]int) string {
	for k := range m {
		return k
	}
	panic("singularKey undefined on empty map")
}

type apdata struct {
	ConnectNames map[string][]int
}

func readAPFile(name string) (data apdata, err error) {
	const expectedAPFileVersion = 3

	apfile, err := os.Open(name)
	if err != nil {
		return
	}
	defer apfile.Close()
	r := bufio.NewReader(apfile)
	version, err := r.ReadByte()
	if err != nil {
		err = fmt.Errorf("read .archipelago version: %w", err)
		return
	}
	if version != expectedAPFileVersion {
		err = fmt.Errorf(".archipelago file is version %d, expected %d", version, expectedAPFileVersion)
		return
	}
	zr, err := zlib.NewReader(r)
	if err != nil {
		err = fmt.Errorf("decompress .archipelago: %w", err)
		return
	}
	defer zr.Close()
	err = pickle.Decode(zr, &data)
	return
}

var errConnectionLost = errors.New("server stopped responding to pings")

func joinServer(mwserver, mwroom, nickname string) error {
	conn, err := net.Dial("tcp", mwserver)
	if err != nil {
		return err
	}
	defer conn.Close()

	const (
		pingInterval       = 5 * time.Second
		reconnectThreshold = 5
	)

	kill := make(chan struct{})
	defer close(kill)
	outbox := make(chan mwproto.Message)
	defer func() {
		outbox <- mwproto.DisconnectMessage{}
		close(outbox)
	}()
	inbox := make(chan mwproto.Message)

	go sendMessages(conn, outbox)
	go readMessages(conn, inbox, kill)

	outbox <- mwproto.ConnectMessage{}

	for {
		msg, ok := <-inbox
		if !ok {
			return errConnectionLost
		}
		if c, ok := msg.(mwproto.ConnectMessage); ok {
			log.Println("connected to", c.ServerName)
			break
		}
		log.Printf("unexpected message before connect: %#v", msg)
	}

	outbox <- mwproto.ReadyMessage{
		Room:          mwroom,
		Nickname:      nickname,
		ReadyMetadata: []mwproto.KeyValuePair{},
	}

	pingTimer := time.NewTicker(pingInterval)
	defer pingTimer.Stop()
	unansweredPings := 0
waitingToEnterRoom:
	for {
		select {
		case <-pingTimer.C:
			unansweredPings++
			if unansweredPings == reconnectThreshold {
				return errConnectionLost
			}
			outbox <- mwproto.PingMessage{}
		case msg, ok := <-inbox:
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.PingMessage:
				unansweredPings = 0
			case mwproto.ReadyConfirmMessage:
				log.Printf("joined room %s with players %v", mwroom, msg.Names)
				break waitingToEnterRoom
			case mwproto.ReadyDenyMessage:
				log.Printf("denied entry to room %s: %s", mwroom, msg.Description)
			case mwproto.DisconnectMessage:
				return errConnectionLost
			default:
				log.Printf("unexpected message while joining room: %#v", msg)
			}
		}
	}

	for {
		select {
		case <-pingTimer.C:
			unansweredPings++
			if unansweredPings == reconnectThreshold {
				return errConnectionLost
			}
			outbox <- mwproto.PingMessage{}
		case msg, ok := <-inbox:
			if !ok {
				return errConnectionLost
			}
			switch msg := msg.(type) {
			case mwproto.PingMessage:
				log.Println("ping")
				unansweredPings = 0
			case mwproto.DisconnectMessage:
				return errConnectionLost
			default:
				log.Printf("unexpected message while in room: %#v", msg)
			}
		}
	}
}

func sendMessages(conn net.Conn, messages <-chan mwproto.Message) {
	for m := range messages {
		if err := mwproto.Write(conn, m); err != nil {
			log.Println("error sending message:", err)
		}
	}
}

func readMessages(conn net.Conn, messages chan<- mwproto.Message, done <-chan struct{}) {
	for {
		msg, err := mwproto.Read(conn)
		if errors.Is(err, io.EOF) {
			close(messages)
			return
		}
		if err != nil {
			log.Println("error reading message:", err)
			continue
		}
		select {
		case <-done:
			return
		case messages <- msg:
		}
	}
}
