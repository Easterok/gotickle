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

	PingPong *time.Ticker

	Conn *websocket.Conn

	Ctx echo.Context

	mu           sync.Mutex
	MessageToApi []byte
	ApiCancel    context.CancelFunc
}

func NewClient(ctx echo.Context, ws *websocket.Conn) *Client {
	return &Client{
		Incoming: make(chan []byte),
		Outgoing: make(chan []byte),
		PingPong: time.NewTicker(pingPeriod),
		Conn:     ws,
		Ctx:      ctx,
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

	c.PingPong.Stop()
	c.Conn.Close()
}

func (c *Client) Writer() {
	defer c.Stop()

	for {
		select {
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

		c.Incoming <- message
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
		in, ok := <-c.Incoming

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
		ctx, cancel := context.WithCancel(context.Background())
		c.ApiCancel = cancel
		c.mu.Unlock()

		go c.TriggerApi(ctx, nextMsg)
	}
}

func (c *Client) TriggerApi(ctx context.Context, msg []byte) {
	randomDuration := 0.1 + rand.Float64()*(2.0-0.1)
	duration := time.Duration(randomDuration * float64(time.Second))

	select {
	case <-time.After(duration):
		resp := generateResponse(msg)

		select {
		case c.Outgoing <- resp:
			c.mu.Lock()
			c.MessageToApi = []byte{}
			c.mu.Unlock()
		default:
			break
		}
	case <-ctx.Done():
		break
	}
}

func socket(c echo.Context) error {
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)

	if err != nil {
		return err
	}

	client := NewClient(c, ws)

	return client.Start()
}

func main() {
	var stop context.CancelFunc

	mainCtx := context.Background()
	mainCtx, stop = signal.NotifyContext(mainCtx, os.Interrupt, syscall.SIGTERM)

	defer stop()

	e := echo.New()

	port := "8080"

	e.GET("/", func(c echo.Context) error {
		return c.File("views/ws.html")
	})

	e.GET("/ws", socket)

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
