package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"easterok.github.com/gotickle/pkg/pprof"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const (
	dotenvErr = "error loading .env file"

	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var upgrader = websocket.Upgrader{}

type SocketMessage struct {
	Hash  string `json:"hash"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type ResponseSocketMessage struct {
	Hash string `json:"hash"`
	Text string `json:"value"`
	Type string `json:"type"`
	Self bool   `json:"self"`
}

func parseText(b []byte) *SocketMessage {
	var msg SocketMessage

	err := json.Unmarshal(b, &msg)

	if err != nil {
		return nil
	}

	return &msg
}

func responseSelfMessage(msg *SocketMessage) *[]byte {
	self := ResponseSocketMessage{
		Self: true,
		Hash: msg.Hash,
		Type: "text",
	}

	b, err := json.Marshal(self)

	if err != nil {
		return nil
	}

	return &b
}

func typing() []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: "-1",
		Text: "Companion is typing",
		Type: "typing",
	}

	b, _ := json.Marshal(self)

	return b
}

func generateResponse(msg []byte) []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: "-1",
		Text: fmt.Sprintf("Response for message: %s", string(msg)),
		Type: "text",
	}

	b, _ := json.Marshal(self)

	return b
}

type Client struct {
	Incoming chan []byte

	Outgoing chan []byte

	Done chan bool

	PingPong *time.Ticker

	Conn *websocket.Conn

	Ctx echo.Context

	Shutdown   context.Context
	ShutdownFn context.CancelFunc

	mu           sync.Mutex
	MessageToApi []byte
	ApiCancel    context.CancelFunc
}

func NewClient(ctx context.Context, e echo.Context, ws *websocket.Conn) *Client {
	shutdown, shutdownFn := context.WithCancel(ctx)

	return &Client{
		Incoming: make(chan []byte),
		Outgoing: make(chan []byte),
		PingPong: time.NewTicker(pingPeriod),
		Conn:     ws,
		Ctx:      e,

		Shutdown:   shutdown,
		ShutdownFn: shutdownFn,
	}
}

func (c *Client) Logger() echo.Logger {
	return (c.Ctx).Logger()
}

func (c *Client) Start() error {
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error { c.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	// fmt.Printf("New client\n")

	go c.Writer()
	go c.Handle()
	go c.Reader()

	return nil
}

func (c *Client) Stop() {
	// fmt.Printf("Closing connection...\n")

	c.ShutdownFn()
	c.PingPong.Stop()
	c.Conn.Close()
}

func (c *Client) Writer() {
	defer c.Stop()

	for {
		select {
		case <-c.Shutdown.Done():
			return
		case msg, ok := <-c.Outgoing:
			if !ok {
				return
			}

			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := c.Conn.WriteMessage(websocket.TextMessage, msg)

			if err != nil {
				c.Logger().Error(err)
				return
			}
		case <-c.PingPong.C:
			// fmt.Println("Sending ping message...")
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Logger().Error(err)
				return
			}
		}
	}
}

func (c *Client) Reader() {
	defer func() {
		close(c.Incoming)
		c.Stop()
	}()

	for {
		_, message, err := c.Conn.ReadMessage()

		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.Logger().Error(err)
			}
			return
		}

		select {
		case c.Incoming <- message:
			//
		case <-c.Shutdown.Done():
			return
		}
	}
}

func (c *Client) Handle() {
	defer func() {
		if c.ApiCancel != nil {
			c.ApiCancel()
		}

		c.Stop()
	}()

	for {
		select {
		case <-c.Shutdown.Done():
			return
		case in, ok := <-c.Incoming:
			if !ok {
				return
			}

			parsed := parseText(in)

			if parsed == nil {
				c.Logger().Error("unable to parse message %s\n", string(in))
				continue
			}

			if parsed.Type != "message" {
				continue
			}

			self := responseSelfMessage(parsed)

			if self == nil {
				continue
			}

			c.Outgoing <- *self
			c.Outgoing <- typing()

			if c.ApiCancel != nil {
				c.ApiCancel()
			}

			c.mu.Lock()
			nextMsg := append(c.MessageToApi, []byte("\n"+parsed.Value)...)
			c.MessageToApi = nextMsg
			ctx, cancel := context.WithCancel(c.Shutdown)
			c.ApiCancel = cancel
			c.mu.Unlock()

			go c.TriggerApi(ctx, nextMsg)
		}
	}
}

func (c *Client) TriggerApi(ctx context.Context, msg []byte) {
	randomDuration := 0.1 + rand.Float64()*(2.0-0.1)
	duration := time.Duration(randomDuration * float64(time.Second))

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(duration):
			resp := generateResponse(msg)

			select {
			case c.Outgoing <- resp:
				c.mu.Lock()
				c.MessageToApi = []byte{}
				c.mu.Unlock()
			default:
				return
			}
		}
	}
}

func socket(ctx context.Context) func(c echo.Context) error {
	return func(c echo.Context) error {
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)

		if err != nil {
			return err
		}

		client := NewClient(ctx, c, ws)

		return client.Start()
	}
}

func main() {
	var stop context.CancelFunc

	mainCtx := context.Background()
	mainCtx, stop = signal.NotifyContext(mainCtx, os.Interrupt, syscall.SIGTERM)

	defer stop()

	e := echo.New()

	port := "8080"

	pprof.Register(e)

	e.GET("/", func(c echo.Context) error {
		return c.File("views/ws.html")
	})

	e.GET("/ws", socket(mainCtx))

	go func() {
		if err := e.Start(fmt.Sprintf(":%s", port)); err != nil {
			e.Logger.Fatal("Shutting down the server")
		}
	}()

	<-mainCtx.Done()

	if err := e.Shutdown(mainCtx); err != nil {
		e.Logger.Fatal(err)
	}
}
