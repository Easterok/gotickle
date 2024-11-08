package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	_ "net/http/pprof"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
)

const (
	dotenvErr = "error loading .env file"

	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

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

func generateResponse(msg string) []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: "-1",
		Text: msg,
		Type: "text",
	}

	b, _ := json.Marshal(self)

	return b
}

type Client struct {
	Incoming chan []byte

	Outgoing chan []byte

	Done chan bool

	Conn *websocket.Conn

	Ctx echo.Context

	HttpClient *http.Client

	Shutdown   context.Context
	ShutdownFn context.CancelFunc

	MessageToApi []byte
	ApiCancel    context.CancelFunc
}

func NewClient(ws *websocket.Conn) *Client {
	shutdown, shutdownFn := context.WithCancel(context.Background())

	return &Client{
		Incoming: make(chan []byte, 256),
		Outgoing: make(chan []byte, 256),
		Conn:     ws,

		HttpClient: &http.Client{},

		Shutdown:   shutdown,
		ShutdownFn: shutdownFn,
	}
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
	c.Conn.Close()
}

func (c *Client) Writer() {
	pingPong := time.NewTicker(pingPeriod)

	defer func() {
		pingPong.Stop()
		c.Stop()
	}()

	for {
		select {
		case <-c.Shutdown.Done():
			return
		case msg, ok := <-c.Outgoing:
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}

			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := c.Conn.WriteMessage(websocket.TextMessage, msg)

			if err != nil {
				fmt.Println(err)
				return
			}
		case <-pingPong.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				fmt.Println(err)
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

outer:
	for {
		_, message, err := c.Conn.ReadMessage()

		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				fmt.Println(err)
			}
			break
		}

		select {
		case c.Incoming <- message:
		case <-c.Shutdown.Done():
			break outer
		default:
			break outer
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

outer:
	for {
		select {
		case <-c.Shutdown.Done():
			break outer
		case in, ok := <-c.Incoming:
			if !ok {
				break outer
			}

			parsed := parseText(in)

			if parsed == nil {
				fmt.Printf("unable to parse message %s\n", string(in))
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

			nextMsg := append(c.MessageToApi, []byte("\n"+parsed.Value)...)
			c.MessageToApi = nextMsg
			ctx, cancel := context.WithCancel(context.Background())
			c.ApiCancel = cancel

			go c.TriggerApi(ctx, nextMsg)
		}
	}
}

type ApiResponse struct {
	Response string `json:"response"`
}

func (c *Client) TriggerApi(ctx context.Context, msg []byte) {
	data, err := json.Marshal(map[string]string{
		"messages": string(msg),
	})

	if err != nil {
		fmt.Printf("Failed to create json %s", err.Error())
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", os.Getenv("API_ENDPOINT"), bytes.NewBuffer(data))

	if err != nil {
		fmt.Printf("Failed to create request %s", err.Error())
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		if ctx.Err() == context.Canceled {
			fmt.Println("Request was canceled")
		} else {
			fmt.Println("Error making request:", err)
		}
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return
	}

	d := new(ApiResponse)
	err = json.Unmarshal(body, &d)

	if err != nil {
		fmt.Printf("Failed to unmarshal response %s\n", err.Error())
		return
	}

outer:
	for {
		select {
		case <-c.Shutdown.Done():
			break outer
		case c.Outgoing <- generateResponse(d.Response):
			c.MessageToApi = []byte{}
			break outer
		default:
			break outer
		}
	}
}

func socket(w http.ResponseWriter, r *http.Request) {
	ws, err := wsUpgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)

		return
	}

	client := NewClient(ws)

	client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error { client.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	go client.Writer()
	go client.Handle()
	go client.Reader()
}

func main() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal(err)
	}

	port := "8080"

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

	http.HandleFunc("/ws", socket)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}
