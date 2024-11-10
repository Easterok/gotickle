package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "net/http/pprof"

	"easterok.github.com/gotickle/pkg/stats"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	WriteBufferPool: &sync.Pool{},
}

var statistic = stats.NewStatsWithConfig(stats.StatsConfig{
	SnapshotInterval: time.Second * 10,
	SnapshotCallback: func(s *stats.Stats) {
		fmt.Printf("-----\nTime:%s\nTotalConnections: %d\nLiveConnections: %d\nMessagesSent: %d\nMessagesReceived: %d\nApiErrors: %d\n-----\n", time.Now().UTC().Format("2006-01-02 15:04:05"), s.TotalConn, s.LiveConn, s.MessagesSent, s.MessagesReceived, s.ErrorCount)
	},
})

type Client struct {
	conn *websocket.Conn

	outgoing chan []byte
}

type customContext struct {
	context.Context
	ch <-chan struct{}
}

func (c customContext) Done() <-chan struct{} {
	return c.ch
}

func (c customContext) Err() error {
	select {
	case <-c.ch:
		return context.Canceled
	default:
		return nil
	}
}

func newClient(conn *websocket.Conn) *Client {
	return &Client{
		conn:     conn,
		outgoing: make(chan []byte, 256),
	}
}

func (c *Client) write(ctx context.Context) {
	pingPong := time.NewTicker(pingPeriod)

	defer pingPong.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.outgoing:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := c.conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				fmt.Printf("write: %s\n", err.Error())
				return
			}
			atomic.AddInt64(&statistic.MessagesSent, 1)

		case <-pingPong.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) read() {
	defer close(c.outgoing)
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			fmt.Printf("read: %s\n", err.Error())
			return
		}
		atomic.AddInt64(&statistic.MessagesReceived, 1)

		c.outgoing <- msg
	}
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	go func() {
		ch := make(chan struct{}, 1)
		ctx := customContext{
			ch: ch,
		}
		defer func() {
			ch <- struct{}{}
			conn.Close()
			close(ch)
			atomic.AddInt64(&statistic.LiveConn, -1)
		}()

		client := newClient(conn)
		client.conn.SetReadDeadline(time.Now().Add(pongWait))
		client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
		atomic.AddInt64(&statistic.LiveConn, 1)
		atomic.AddInt64(&statistic.TotalConn, 1)

		go client.write(ctx)
		client.read()
	}()
}

func main() {
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err != nil {
		log.Fatal(err)
	}
	rLimit.Cur = rLimit.Max
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit); err != nil {
		log.Fatal(err)
	}

	defer statistic.Stop()
	go statistic.Start()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		http.ServeFile(w, r, "views/ws.html")
	})
	http.HandleFunc("/ws", serveWS)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
